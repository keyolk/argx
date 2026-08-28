package argocd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Exec attaches to a container's shell through Argo CD's terminal endpoint.
//
// Argo CD proxies the exec rather than argx talking to Kubernetes directly,
// which is the point: the session inherits Argo CD's RBAC and shows up in its
// audit log. A kubectl exec would do neither, and would need a kubeconfig
// context that argx has no way to map a destination cluster onto.
//
// The endpoint is a WebSocket at /terminal, not part of the REST API, and it
// speaks its own small JSON protocol — see TerminalMessage. Both are read from
// Argo CD's own server (server/application/terminal.go) rather than guessed.

// ExecRequest identifies what to attach to.
type ExecRequest struct {
	App          string
	AppNamespace string
	Project      string
	Namespace    string
	Pod          string
	Container    string
	// Shell is the command to run. Empty lets the server try each shell it
	// allows in turn, which is what the Argo CD UI does.
	Shell string
	// Rows and Cols size the pty at startup.
	Rows, Cols uint16
}

// terminalMessage is Argo CD's WebSocket frame.
//
// Operation is "stdin", "stdout", or "resize"; the field names and their
// meanings are fixed by the server, so they are mirrored exactly.
type terminalMessage struct {
	Operation string `json:"operation"`
	Data      string `json:"data"`
	Rows      uint16 `json:"rows"`
	Cols      uint16 `json:"cols"`
}

// ExecSession is a live shell.
//
// It is an io.ReadWriter over the container's stdin and stdout, plus a Resize.
// Nothing about it is Bubble Tea aware — the TUI drives it, so the transport
// stays testable without a terminal.
type ExecSession struct {
	conn *websocket.Conn
	// out buffers stdout that arrived before the reader asked for it, since a
	// WebSocket frame does not align with a Read's buffer.
	pending []byte
	closed  bool
}

// Exec opens a shell in a container.
func (c *Client) Exec(ctx context.Context, req ExecRequest) (*ExecSession, error) {
	if req.Pod == "" || req.Container == "" || req.App == "" || req.Namespace == "" {
		return nil, fmt.Errorf("exec: pod, container, application, and namespace are all required")
	}
	// The server rejects a request missing the project, and its error says only
	// "Missing required parameters" — catching it here says which one.
	if req.Project == "" {
		return nil, fmt.Errorf("exec: the application's project is required")
	}

	q := url.Values{}
	q.Set("pod", req.Pod)
	q.Set("container", req.Container)
	q.Set("appName", req.App)
	q.Set("projectName", req.Project)
	q.Set("namespace", req.Namespace)
	if req.AppNamespace != "" {
		q.Set("appNamespace", req.AppNamespace)
	}
	if req.Shell != "" {
		q.Set("shell", req.Shell)
	}

	// ws:// for http, wss:// for https — the scheme has to match or the
	// handshake is refused.
	base := c.ctx.BaseURL()
	wsURL := "wss://" + strings.TrimPrefix(base, "https://")
	if strings.HasPrefix(base, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(base, "http://")
	}
	wsURL += "/terminal?" + q.Encode()

	dialer := &websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
	if c.ctx.Insecure {
		tr := c.http.Transport.(*http.Transport)
		dialer.TLSClientConfig = tr.TLSClientConfig
	}

	// The session token goes in a cookie, not an Authorization header: that is
	// what the terminal handler's auth middleware reads.
	hdr := http.Header{}
	hdr.Set("Cookie", "argocd.token="+c.ctx.Token)

	conn, resp, err := dialer.DialContext(ctx, wsURL, hdr)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, &APIError{
				Status: resp.StatusCode,
				Msg:    apiMessage(body),
				Path:   "/terminal",
				Server: c.ctx.Server,
			}
		}
		return nil, fmt.Errorf("connect to %s: %w", req.Pod, err)
	}

	s := &ExecSession{conn: conn}
	if req.Rows > 0 && req.Cols > 0 {
		if err := s.Resize(req.Rows, req.Cols); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// Read returns whatever the container has written.
//
// A WebSocket frame does not align with the caller's buffer, so the remainder
// of a frame is held until the next Read rather than discarded.
func (s *ExecSession) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		if s.closed {
			return 0, io.EOF
		}
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return 0, io.EOF
			}
			return 0, err
		}
		var msg terminalMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// A frame argx cannot parse is the server's keepalive or a protocol
			// change; skipping it beats tearing the session down.
			continue
		}
		if msg.Operation != "stdout" || msg.Data == "" {
			continue
		}
		s.pending = []byte(msg.Data)
	}

	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// Write sends input to the container.
func (s *ExecSession) Write(p []byte) (int, error) {
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	msg := terminalMessage{Operation: "stdin", Data: string(p)}
	b, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize tells the container's pty its new size.
//
// Without this a full-screen program inside the container draws to the wrong
// dimensions — the reason `vi` in a terminal that was resized looks broken.
func (s *ExecSession) Resize(rows, cols uint16) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	b, err := json.Marshal(terminalMessage{Operation: "resize", Rows: rows, Cols: cols})
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, b)
}

// Close ends the session.
func (s *ExecSession) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	// A close frame first, so the server tears down the exec rather than
	// waiting for the connection to time out.
	_ = s.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	return s.conn.Close()
}

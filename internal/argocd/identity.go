package argocd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// What a context's credential actually is.
//
// A fleet session holds several tokens at once, each of which may be a
// different account with different permissions on a different server. Which one
// is being used is invisible until something is refused — and a refusal after
// pressing `s` is a worse way to find out than a line that says so up front.
//
// Two sources answer this, and they answer different questions:
//
//   - The token itself says who issued it and when. It is read locally, so it
//     works for an expired or rejected token, which is exactly when the reader
//     needs to know.
//   - The server says who it thinks the caller is, which is the authority — a
//     token can carry claims the server maps onto a different account entirely,
//     and SSO group membership exists nowhere else.

// TokenClaims is what a credential says about itself.
//
// Deliberately not every claim: this is rendered on screen, and a claim whose
// value is itself credential-shaped (at_hash, nonce) has no business being
// displayed even redacted.
type TokenClaims struct {
	// Subject is the account, e.g. "admin:apiKey" for a local API key or an
	// SSO subject id.
	Subject string
	// Issuer is "argocd" for a local account, or the OIDC provider's URL.
	Issuer string
	// Email and Name come from an SSO provider; both are empty for a local
	// account.
	Email string
	Name  string
	// Groups are the SSO groups the token carries. Argo CD's RBAC maps these,
	// so they are what decides what the session can do.
	Groups []string

	// IssuedAt and ExpiresAt are zero when the token omits them. An API key
	// generated without a lifetime has no expiry at all, which is worth being
	// able to say rather than leaving blank.
	IssuedAt  time.Time
	ExpiresAt time.Time

	// ID is the token's jti. Argo CD lists API keys by it, so it is the one
	// value that connects a token in hand to an entry in the account's key
	// list — the closest thing to a token's name.
	ID string
}

// Local reports whether this is an Argo CD account rather than an SSO identity.
func (t TokenClaims) Local() bool { return t.Issuer == "argocd" || t.Issuer == "" }

// APIKey reports whether the credential is a generated API key rather than a
// login session. Argo CD encodes this in the subject.
func (t TokenClaims) APIKey() bool {
	return strings.HasSuffix(t.Subject, ":apiKey")
}

// Account is the account name, without the ":apiKey" / ":login" suffix Argo CD
// appends to distinguish how the token was obtained.
func (t TokenClaims) Account() string {
	if i := strings.LastIndex(t.Subject, ":"); i > 0 {
		return t.Subject[:i]
	}
	return t.Subject
}

// Expired reports whether the token's own expiry has passed. A token with no
// expiry never reports expired — the absence of one is a separate fact, and
// conflating them would flag every API key as broken.
func (t TokenClaims) Expired(at time.Time) bool {
	return !t.ExpiresAt.IsZero() && t.ExpiresAt.Before(at)
}

// ParseTokenClaims reads a JWT's payload without verifying it.
//
// Verification is the server's job and needs its signing key, which argx does
// not have. Reading unverified is safe for what this is used for — telling the
// reader what credential they are holding — and is the only thing that works
// when the token has expired, which is when the question gets asked.
func ParseTokenClaims(token string) (TokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return TokenClaims{}, fmt.Errorf("not a JWT: %d segments", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return TokenClaims{}, fmt.Errorf("payload is not base64url: %w", err)
	}

	var c struct {
		Sub    string   `json:"sub"`
		Iss    string   `json:"iss"`
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
		Iat    float64  `json:"iat"`
		Exp    float64  `json:"exp"`
		Jti    string   `json:"jti"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return TokenClaims{}, fmt.Errorf("payload is not JSON: %w", err)
	}

	out := TokenClaims{
		Subject: c.Sub, Issuer: c.Iss, Email: c.Email, Name: c.Name,
		Groups: c.Groups, ID: c.Jti,
	}
	if c.Iat > 0 {
		out.IssuedAt = time.Unix(int64(c.Iat), 0)
	}
	if c.Exp > 0 {
		out.ExpiresAt = time.Unix(int64(c.Exp), 0)
	}
	return out, nil
}

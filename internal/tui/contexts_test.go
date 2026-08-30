package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

func ctxRow(name string, mutate func(*contextRow)) contextRow {
	r := contextRow{
		name: name, server: name, source: "pass:infra/argocd/token",
		claims: argocd.TokenClaims{Subject: "admin:apiKey", Issuer: "argocd"},
		user:   &argocd.UserInfo{LoggedIn: true, Username: "admin", Iss: "argocd"},
		perms: []contextPerm{
			{"read apps", true}, {"sync", true}, {"edit spec", true},
			{"rollback", true}, {"logs", true}, {"exec", true}, {"projects", true},
		},
	}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

func ctxModel(t *testing.T, rows ...contextRow) *Model {
	t.Helper()
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 26})
	m.ctxRows = rows
	m.ctxLoaded = true
	// push, not an assignment: the view is entered from the application list
	// and Esc has to have somewhere to return to.
	m.push(screenContexts)
	return m
}

// The credential never reaches the screen. The view exists to describe a token,
// which is not the same as showing one — a token on screen during a call is a
// token that leaves the machine.
func TestTheTokenItselfIsNeverRendered(t *testing.T) {
	const secret = "eyJhbGciOiJIUzI1NiJ9.SUPERSECRETPAYLOAD.sig"

	claims, _ := argocd.ParseTokenClaims(secret)
	m := ctxModel(t, ctxRow("prod", func(r *contextRow) { r.claims = claims }))

	out := stripANSI(m.View())
	if strings.Contains(out, "SUPERSECRETPAYLOAD") || strings.Contains(out, secret) {
		t.Fatalf("the credential appeared on screen:\n%s", out)
	}
	m.ctxDetail = true
	if out := stripANSI(m.View()); strings.Contains(out, "SUPERSECRETPAYLOAD") {
		t.Fatalf("the credential appeared in the detail panel:\n%s", out)
	}
}

// Where a credential comes from is the one value here that names a place to fix
// when it is wrong, so it has to be visible.
func TestTheTokenSourceIsShown(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", func(r *contextRow) {
		r.source = "pass:infra/argocd/prod"
	}))
	m.ctxDetail = true

	if out := stripANSI(m.View()); !strings.Contains(out, "pass:infra/argocd/prod") {
		t.Errorf("the source should say where the credential comes from:\n%s", out)
	}
}

// An SSO identity is what the reader recognises; an opaque subject id is not.
func TestSSOIdentityLeadsWithTheEmail(t *testing.T) {
	r := ctxRow("prod", func(r *contextRow) {
		r.claims = argocd.TokenClaims{
			Subject: "CgVzb21lEgRsZGFw", Issuer: "https://sso.example.com/dex",
			Email: "someone@example.com", Groups: []string{"platform"},
		}
		r.user = &argocd.UserInfo{LoggedIn: true, Username: "someone@example.com",
			Iss: "https://sso.example.com/dex", Groups: []string{"platform"}}
	})
	if got := r.identity(); got != "someone@example.com" {
		t.Errorf("identity = %q, want the email over the subject id", got)
	}
	if got := r.authKind(); got != "SSO" {
		t.Errorf("kind = %q, want SSO", got)
	}
	// The issuer is rendered as its host — the URL's path says nothing.
	if got := r.issuer(); got != "sso.example.com" {
		t.Errorf("issuer = %q, want the provider's host", got)
	}

	m := ctxModel(t, r)
	out := stripANSI(m.View())
	if !strings.Contains(out, "someone@example.com") || !strings.Contains(out, "SSO") {
		t.Errorf("the list should name the SSO identity:\n%s", out)
	}
	m.ctxDetail = true
	if out := stripANSI(m.View()); !strings.Contains(out, "platform") {
		t.Errorf("SSO groups decide what RBAC allows and should be shown:\n%s", out)
	}
}

// A local account is not an issuer worth naming: "argocd" says nothing the kind
// does not already say.
func TestLocalAccountHasNoIssuerToShow(t *testing.T) {
	r := ctxRow("prod", nil)
	if got := r.issuer(); got != "" {
		t.Errorf("issuer = %q, want nothing for a local account", got)
	}
	if got := r.authKind(); got != "API key" {
		t.Errorf("kind = %q, want API key", got)
	}
}

// An API key with no expiry is a deliberate choice, not missing data, and its
// age is worth seeing — the fleet has one that is 23 months old.
func TestAnOldAPIKeyReportsItsAge(t *testing.T) {
	now := time.Now()
	r := ctxRow("prod", func(r *contextRow) {
		r.claims.IssuedAt = now.Add(-340 * 24 * time.Hour)
	})

	text, warn := r.age(now)
	if !strings.Contains(text, "no expiry") {
		t.Errorf("age = %q, want it to say there is no expiry", text)
	}
	if !strings.Contains(text, "11mo") {
		t.Errorf("age = %q, want the key's age", text)
	}
	if warn {
		t.Error("no expiry is not a warning — that would flag every API key as broken")
	}
}

// An expired credential is the reason someone opens this view.
func TestExpiredCredentialIsFlagged(t *testing.T) {
	now := time.Now()
	r := ctxRow("prod", func(r *contextRow) {
		r.claims.ExpiresAt = now.Add(-3 * 24 * time.Hour)
		r.err = errors.New("401 Unauthorized: token is expired")
		r.user = nil
	})

	text, warn := r.age(now)
	if !warn || !strings.Contains(text, "EXPIRED") {
		t.Errorf("age = %q warn=%v, want an expired credential flagged", text, warn)
	}

	m := ctxModel(t, r)
	out := stripANSI(m.View())
	if !strings.Contains(out, "EXPIRED") {
		t.Errorf("the list should flag the expired credential:\n%s", out)
	}
	if !strings.Contains(out, "401") {
		t.Errorf("the server's own words explain it best:\n%s", out)
	}
	if !strings.Contains(out, "1 unusable") {
		t.Errorf("the status line should count it:\n%s", out)
	}
}

// A credential that expires soon warns before something fails halfway.
func TestSoonToExpireWarns(t *testing.T) {
	now := time.Now()
	r := ctxRow("prod", func(r *contextRow) { r.claims.ExpiresAt = now.Add(20 * time.Minute) })
	if _, warn := r.age(now); !warn {
		t.Error("twenty minutes left should warn")
	}
	r2 := ctxRow("prod", func(r *contextRow) { r.claims.ExpiresAt = now.Add(6 * time.Hour) })
	if _, warn := r2.age(now); warn {
		t.Error("six hours left is not urgent")
	}
}

// What a session cannot do is the useful half. A list of everything it can do
// is noise on an admin token and buries the one refusal that matters.
func TestOnlyDeniedPermissionsAreCalledOut(t *testing.T) {
	full := ctxRow("prod", nil)
	if d := full.denied(); len(d) != 0 {
		t.Errorf("denied = %v, want nothing for a full-access session", d)
	}

	limited := ctxRow("prod", func(r *contextRow) {
		r.perms = []contextPerm{
			{"read apps", true}, {"sync", true}, {"edit spec", false},
			{"rollback", false}, {"logs", true}, {"exec", false}, {"projects", true},
		}
	})
	d := limited.denied()
	if len(d) != 3 {
		t.Fatalf("denied = %v, want the three refusals", d)
	}

	m := ctxModel(t, limited)
	out := stripANSI(m.View())
	if !strings.Contains(out, "cannot edit spec, rollback, exec") {
		t.Errorf("the list should name what this session cannot do:\n%s", out)
	}
	if !strings.Contains(out, "1 limited") {
		t.Errorf("the status line should count it:\n%s", out)
	}
}

// A 200 that says loggedIn:false is an anonymous session, which looks like
// success right up until something is refused.
func TestAnonymousSessionIsNotSuccess(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", func(r *contextRow) {
		r.user = &argocd.UserInfo{LoggedIn: false}
	}))
	m.ctxDetail = true

	if out := stripANSI(m.View()); !strings.Contains(out, "not logged in") {
		t.Errorf("an anonymous session should say so:\n%s", out)
	}
}

// TLS verification being off is a property of the connection worth carrying.
func TestInsecureConnectionIsFlagged(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", func(r *contextRow) { r.insecure = true }))
	if out := stripANSI(m.View()); !strings.Contains(out, "TLS verification off") {
		t.Errorf("an unverified connection should say so:\n%s", out)
	}
}

// A server argx cannot authenticate to is the reason anybody opened this view,
// so it leads.
func TestBrokenContextsLead(t *testing.T) {
	rows := []contextRow{
		ctxRow("fine-1", nil),
		ctxRow("broken", func(r *contextRow) { r.err = errors.New("401") }),
		ctxRow("fine-2", nil),
	}
	sortContextRows(rows)
	if rows[0].name != "broken" {
		t.Errorf("order = %s…, want the broken server first", rows[0].name)
	}
	// Otherwise fleet order is preserved — a list that reorders itself between
	// visits costs the reader their place.
	if rows[1].name != "fine-1" || rows[2].name != "fine-2" {
		t.Errorf("the working servers should keep fleet order, got %s, %s",
			rows[1].name, rows[2].name)
	}
}

// A token argx cannot read still describes what it can: the source, and the
// server's own answer.
func TestUnreadableTokenStillShowsTheRest(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", func(r *contextRow) {
		r.claims = argocd.TokenClaims{}
		r.claimsErr = errors.New("not a JWT: 1 segments")
	}))
	m.ctxDetail = true

	out := stripANSI(m.View())
	if !strings.Contains(out, "not a readable JWT") {
		t.Errorf("an unreadable token should say so:\n%s", out)
	}
	if !strings.Contains(out, "pass:infra/argocd/token") {
		t.Errorf("the source is still known and still useful:\n%s", out)
	}
	if !strings.Contains(out, "admin") {
		t.Errorf("the server's answer does not depend on argx parsing the token:\n%s", out)
	}
}

// C reaches the view from anywhere and toggles back.
func TestCTogglesTheContextView(t *testing.T) {
	m := newTestModel(t, "web")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})

	m.Update(key("C"))
	if m.screen != screenContexts {
		t.Fatalf("screen = %v, want the context view", m.screen)
	}
	m.ctxLoaded = true
	m.Update(key("C"))
	if m.screen != screenApps {
		t.Errorf("screen = %v, want back at the application list", m.screen)
	}
}

// Enter opens the detail and Esc returns to the list, rather than leaving the
// view entirely — the list is where you came from.
func TestDetailIsItsOwnLevel(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", nil), ctxRow("dev", nil))

	m.Update(key("enter"))
	if !m.ctxDetail {
		t.Fatal("enter should open the detail")
	}
	m.Update(key("esc"))
	if m.ctxDetail {
		t.Fatal("esc should close the detail")
	}
	if m.screen != screenContexts {
		t.Errorf("screen = %v, want to still be in the context view", m.screen)
	}
}

// The permission list is long enough to overflow a short terminal, and a panel
// that silently ends has told the reader those permissions do not exist.
func TestOverflowingDetailSaysThereIsMore(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", nil))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m.ctxDetail = true

	if out := stripANSI(m.View()); !strings.Contains(out, "more line(s)") {
		t.Errorf("a truncated panel should say so:\n%s", out)
	}
}

// Navigation stays inside the list.
func TestContextListNavigates(t *testing.T) {
	m := ctxModel(t, ctxRow("a", nil), ctxRow("b", nil), ctxRow("c", nil))

	m.Update(key("j"))
	m.Update(key("j"))
	m.Update(key("j"))
	if m.ctxCur != 2 {
		t.Errorf("cursor = %d, want it clamped to the last row", m.ctxCur)
	}
	m.Update(key("g"))
	if m.ctxCur != 0 {
		t.Errorf("cursor = %d, want the top", m.ctxCur)
	}
}

// Leaving by C must not leave the detail panel armed for next time.
func TestLeavingResetsTheDetailPanel(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", nil))
	m.Update(key("enter"))
	if !m.ctxDetail {
		t.Fatal("enter should open the detail")
	}
	m.Update(key("C"))
	if m.screen != screenApps || m.ctxDetail {
		t.Errorf("screen = %v detail = %v, want back at the list with the panel closed",
			m.screen, m.ctxDetail)
	}
	m.Update(key("C"))
	if m.ctxDetail {
		t.Error("returning should land on the list, not the panel you left open")
	}
}

// The view is loaded on entry, not at startup: it is two requests per server
// and most sessions never ask.
func TestContextsAreLoadedLazily(t *testing.T) {
	m := newTestModel(t, "web")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})

	if m.ctxLoaded {
		t.Fatal("nothing should have been asked before the view was opened")
	}
	_, cmd := m.Update(key("C"))
	if cmd == nil {
		t.Error("opening the view should ask the servers")
	}

	// Re-entering does not ask again — the answer does not change mid-session.
	m.Update(contextsMsg{rows: []contextRow{ctxRow("prod", nil)}})
	m.Update(key("C"))
	if _, cmd := m.Update(key("C")); cmd != nil {
		t.Error("the answer is already known; asking again is r's job")
	}
}

// Until the answers arrive the view says what it is doing, rather than showing
// an empty list that reads as "no servers".
func TestLoadingContextsSaysSo(t *testing.T) {
	m := newTestModel(t, "web")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(key("C"))

	if out := stripANSI(m.View()); !strings.Contains(out, "asking each server") {
		t.Errorf("the view should say it is loading:\n%s", out)
	}
}

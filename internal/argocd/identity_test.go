package argocd

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// jwt builds an unsigned JWT with the given payload. The signature is never
// checked — argx has no signing key and does not need one to read claims.
func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." +
		enc(claims) + ".signature"
}

// A local API key is the common case in this fleet: no expiry, no email, and a
// subject that says how it was obtained.
func TestLocalAPIKeyClaims(t *testing.T) {
	tok := jwt(t, map[string]any{
		"sub": "admin:apiKey", "iss": "argocd",
		"iat": 1727370205, "jti": "7f9e727a-1ac8-4596-a9d7-5c84b661fe4f",
	})

	c, err := ParseTokenClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Local() {
		t.Error("issuer argocd is a local account")
	}
	if !c.APIKey() {
		t.Error("an :apiKey subject is an API key, not a login session")
	}
	if c.Account() != "admin" {
		t.Errorf("account = %q, want admin without the suffix", c.Account())
	}
	if c.IssuedAt.IsZero() {
		t.Error("iat should be read — an eleven-month-old token is worth noticing")
	}
	if !c.ExpiresAt.IsZero() {
		t.Error("this key has no expiry; inventing one would be a lie")
	}
	// No expiry must not read as expired, or every API key looks broken.
	if c.Expired(time.Now()) {
		t.Error("a token with no expiry never expires")
	}
}

// An SSO identity carries the fields that decide what the session can do.
func TestSSOClaims(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	tok := jwt(t, map[string]any{
		"sub": "CgVhZG1pbhIF", "iss": "https://sso.example.com/dex",
		"email": "someone@example.com", "name": "Someone",
		"groups": []string{"platform", "sre"},
		"exp":    exp,
	})

	c, err := ParseTokenClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Local() {
		t.Error("an OIDC issuer is not a local account")
	}
	if c.APIKey() {
		t.Error("an SSO session is not an API key")
	}
	if c.Email != "someone@example.com" || c.Name != "Someone" {
		t.Errorf("identity = %q / %q, want the SSO email and name", c.Email, c.Name)
	}
	if len(c.Groups) != 2 {
		t.Errorf("groups = %v, want both — Argo CD's RBAC maps these", c.Groups)
	}
	if c.ExpiresAt.IsZero() {
		t.Error("an SSO session expires and should say when")
	}
}

// An expired token still parses. Verification is the server's job, and reading
// locally is the only thing that works at the moment the question gets asked.
func TestExpiredTokenStillParses(t *testing.T) {
	tok := jwt(t, map[string]any{
		"sub": "admin:login", "iss": "argocd",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	c, err := ParseTokenClaims(tok)
	if err != nil {
		t.Fatal("an expired token must still be readable — that is when it matters")
	}
	if !c.Expired(time.Now()) {
		t.Error("the token should report itself expired")
	}
	if c.APIKey() {
		t.Error("a :login subject is a session, not an API key")
	}
}

func TestMalformedTokensAreErrors(t *testing.T) {
	for _, tok := range []string{
		"", "not-a-jwt", "a.b", "a.b.c.d",
		"header.!!!notbase64!!!.sig",
	} {
		if _, err := ParseTokenClaims(tok); err == nil {
			t.Errorf("%q should not parse", tok)
		}
	}
	// Valid base64 that is not JSON.
	bad := base64.RawURLEncoding.EncodeToString([]byte("nonsense"))
	if _, err := ParseTokenClaims("h." + bad + ".s"); err == nil {
		t.Error("a non-JSON payload should not parse")
	}
}

// A subject with no suffix is the account itself.
func TestAccountWithoutASuffix(t *testing.T) {
	tok := jwt(t, map[string]any{"sub": "some-account", "iss": "argocd"})
	c, _ := ParseTokenClaims(tok)
	if c.Account() != "some-account" {
		t.Errorf("account = %q, want the subject unchanged", c.Account())
	}
	if c.APIKey() {
		t.Error("no :apiKey suffix, so not an API key")
	}
}

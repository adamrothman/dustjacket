package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

const (
	issuer   = "https://dustjacket.test"
	callback = "https://claude.ai/api/mcp/auth_callback"
	verifier = "a-pkce-verifier-that-is-long-enough-to-pass-the-43-char-minimum"
)

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// clock is a settable time for Server.Now.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// newServer is a server whose allowlist holds user 7, "adam", and whose
// one admin is user 1.
func newServer() (*Server, *store.Memory, *clock) {
	st := store.NewMemory()
	clk := &clock{t: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	if err := st.Put(context.Background(), Allowlist{Version: 1, Users: map[string]AllowedUser{"7": {ID: "7", Username: "adam"}}}); err != nil {
		panic(err)
	}
	return &Server{Store: st, Sealer: sealer.NewLocal(), Issuer: issuer, AllowedRedirectHosts: []string{"claude.ai", "claude.com"}, Admins: []string{"1"}, Now: clk.now}, st, clk
}

// register posts what claude.ai posts and returns the client ID.
func register(t *testing.T, s *Server, redirect string) (int, map[string]any) {
	t.Helper()
	body := `{"client_name":"Claude","redirect_uris":["` + redirect + `"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}`
	w := httptest.NewRecorder()
	s.Register(w, httptest.NewRequest("POST", "/oauth/register", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func authorizeQuery(clientID string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {callback},
		"state":                 {"st-1"},
		"code_challenge":        {challenge(verifier)},
		"code_challenge_method": {"S256"},
		"scope":                 {"hardcover"},
		"resource":              {issuer + "/mcp"},
	}
}

func parse(s *Server, q url.Values) (*AuthorizeRequest, error) {
	return s.ParseAuthorize(httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil))
}

func TestMetadata(t *testing.T) {
	s, _, _ := newServer()
	w := httptest.NewRecorder()
	s.Metadata(w, httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil))
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["issuer"] != issuer || m["authorization_endpoint"] != issuer+"/oauth/authorize" || m["scopes_supported"].([]any)[0] != "hardcover" {
		t.Fatalf("metadata: %v", m)
	}
	w = httptest.NewRecorder()
	s.ProtectedResourceMetadata(w, httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["resource"] != issuer+"/mcp" || m["authorization_servers"].([]any)[0] != issuer {
		t.Fatalf("resource metadata: %v", m)
	}
}

func TestRegisterRestrictsHostsAndExpiresUnusedClients(t *testing.T) {
	s, st, clk := newServer()
	code, out := register(t, s, callback)
	if code != http.StatusCreated || out["client_id"] == "" {
		t.Fatalf("register: %d %v", code, out)
	}
	var c Client
	if err := st.Get(t.Context(), store.Key{PK: "OAUTHCLIENT#" + out["client_id"].(string), SK: "META"}, &c); err != nil {
		t.Fatal(err)
	}
	if c.Expires == nil || !c.Expires.Equal(clk.t.Add(24*time.Hour)) {
		t.Fatalf("expires: %v", c.Expires)
	}
	for _, bad := range []string{"https://evil.example/cb", "http://claude.ai/cb", "https://claude.ai/cb#frag"} {
		if code, _ := register(t, s, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

func TestParseAuthorize(t *testing.T) {
	s, _, clk := newServer()
	_, out := register(t, s, callback)
	id := out["client_id"].(string)

	req, err := parse(s, authorizeQuery(id))
	if err != nil || req.Client.ID != id || req.RedirectURI != callback || req.State != "st-1" || req.Challenge != challenge(verifier) {
		t.Fatalf("valid request: %+v %v", req, err)
	}
	q := authorizeQuery(id)
	q.Del("scope")
	if _, err := parse(s, q); err != nil {
		t.Fatalf("no scope: %v", err)
	}

	// Errors shown to the person: no request to redirect with.
	for name, q := range map[string]url.Values{
		"unknown client": authorizeQuery("nope"),
		"wrong redirect": func() url.Values { q := authorizeQuery(id); q.Set("redirect_uri", "https://claude.ai/other"); return q }(),
	} {
		if req, err := parse(s, q); err == nil || req != nil {
			t.Errorf("%s: %+v %v", name, req, err)
		}
	}
	// Errors sent back to the client.
	for name, tc := range map[string]struct {
		q    url.Values
		code string
	}{
		"no PKCE":        {func() url.Values { q := authorizeQuery(id); q.Del("code_challenge"); return q }(), "invalid_request"},
		"plain PKCE":     {func() url.Values { q := authorizeQuery(id); q.Set("code_challenge_method", "plain"); return q }(), "invalid_request"},
		"token response": {func() url.Values { q := authorizeQuery(id); q.Set("response_type", "token"); return q }(), "unsupported_response_type"},
		"other scope":    {func() url.Values { q := authorizeQuery(id); q.Set("scope", "hardcover admin"); return q }(), "invalid_scope"},
		"other resource": {func() url.Values { q := authorizeQuery(id); q.Set("resource", "https://elsewhere/mcp"); return q }(), "invalid_target"},
	} {
		req, err := parse(s, tc.q)
		var re *RedirectError
		if !errors.As(err, &re) || re.Code != tc.code || req == nil {
			t.Errorf("%s: %+v %v", name, req, err)
		}
	}

	// A day later, the client nobody connected through is gone.
	clk.t = clk.t.Add(24 * time.Hour)
	if _, err := parse(s, authorizeQuery(id)); err == nil {
		t.Fatal("expired client accepted")
	}
}

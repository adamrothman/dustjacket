package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/reqlog"
	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

const (
	base     = "https://dustjacket.test"
	callback = "https://claude.ai/api/mcp/auth_callback"
	verifier = "a-pkce-verifier-that-is-long-enough-to-pass-the-43-char-minimum"
)

// fakeHardcover answers `me` according to the key.
func fakeHardcover(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
		case "key-adam":
			io.WriteString(w, `{"data":{"me":[{"id":7,"username":"Adam"}]}}`)
		case "key-mallory":
			io.WriteString(w, `{"data":{"me":[{"id":9,"username":"mallory"}]}}`)
		case "key-empty":
			io.WriteString(w, `{"data":{"me":[]}}`)
		case "key-noscope":
			w.WriteHeader(403)
			io.WriteString(w, `{"error":"insufficient_scope","scope":"read:me:content"}`)
		case "key-down":
			w.WriteHeader(503)
		default:
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_token"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type env struct {
	srv   *httptest.Server
	oauth *oauth.Server
	store *store.Memory
	logs  *bytes.Buffer
}

func newEnv(t *testing.T, mcp http.Handler) *env {
	t.Helper()
	st := store.NewMemory()
	oa := &oauth.Server{Store: st, Sealer: sealer.NewLocal(), Issuer: base, AllowedRedirectHosts: []string{"claude.ai", "claude.com"}, Admins: []string{"1"}}
	if _, err := oa.Allow(t.Context(), oauth.AllowedUser{ID: "7", Username: "adam"}); err != nil {
		t.Fatal(err)
	}
	if mcp == nil {
		mcp = http.NotFoundHandler()
	}
	logs := &bytes.Buffer{}
	s := &Server{
		OAuth:     oa,
		Hardcover: &hardcover.Client{URL: fakeHardcover(t).URL},
		MCP:       mcp,
		BaseURL:   base,
		Log:       slog.New(slog.NewTextHandler(logs, nil)),
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{srv: srv, oauth: oa, store: st, logs: logs}
}

// noRedirects is a browser that stops at the redirect to claude.ai.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *env) register(t *testing.T) string {
	t.Helper()
	res, err := http.Post(e.srv.URL+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Claude","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out["client_id"].(string)
}

func authorizeQuery(clientID string) string {
	sum := sha256.Sum256([]byte(verifier))
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {callback},
		"state":                 {"st-1"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"scope":                 {"hardcover"},
	}.Encode()
}

// post sends the connect form as a browser on the connect page would.
func (e *env) post(t *testing.T, origin string, form url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("POST", e.srv.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestConnectPage(t *testing.T) {
	e := newEnv(t, nil)
	q := authorizeQuery(e.register(t))
	res, err := http.Get(e.srv.URL + "/oauth/authorize?" + q)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	body := html.UnescapeString(string(b))
	if res.StatusCode != 200 || !strings.Contains(body, "<strong>Claude</strong>") || !strings.Contains(body, hardcover.KeyURL) || !strings.Contains(body, `name="key" type="password"`) {
		t.Fatalf("connect page: %d %s", res.StatusCode, body)
	}
	// no-referrer would make browsers send "Origin: null" with the form,
	// failing the origin check.
	if !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "default-src 'none'") || res.Header.Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("headers: %v", res.Header)
	}
}

func TestConnectFlow(t *testing.T) {
	e := newEnv(t, nil)
	clientID := e.register(t)
	q := authorizeQuery(clientID)
	// Pasted with a Bearer prefix and spaces.
	res, body := e.post(t, base, url.Values{"q": {q}, "decision": {"connect"}, "key": {"  Bearer key-adam "}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("connect: %d %s", res.StatusCode, body)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), callback+"?") || loc.Query().Get("state") != "st-1" || loc.Query().Get("code") == "" {
		t.Fatalf("redirect: %s", loc)
	}

	// The code exchanges for a token that carries the key.
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier}, "redirect_uri": {callback}}
	tres, err := http.PostForm(e.srv.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer tres.Body.Close()
	var tok map[string]any
	if err := json.NewDecoder(tres.Body).Decode(&tok); err != nil || tres.StatusCode != 200 {
		t.Fatalf("token: %d %v %v", tres.StatusCode, tok, err)
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+tok["access_token"].(string))
	caller, err := e.oauth.Authenticate(r.Context(), r)
	if err != nil || caller.Key != "key-adam" || caller.Username != "Adam" || caller.UserID != "7" {
		t.Fatalf("caller: %+v %v", caller, err)
	}
	if strings.Contains(e.logs.String(), "key-adam") {
		t.Fatalf("the key reached the logs: %s", e.logs)
	}
}

func TestConnectRejections(t *testing.T) {
	e := newEnv(t, nil)
	q := authorizeQuery(e.register(t))
	for _, tc := range []struct {
		name   string
		origin string
		key    string
		status int
		want   string
	}{
		{"no origin", "", "key-adam", 403, "Dustjacket's own page"},
		{"other origin", "https://evil.example", "key-adam", 403, "Dustjacket's own page"},
		{"no key", base, "   ", 400, msgNoKey},
		{"bad key", base, "key-bogus-123", 400, msgBadKey},
		{"empty me", base, "key-empty", 400, msgBadKey},
		{"missing scope", base, "key-noscope", 400, msgNoScope},
		{"hardcover down", base, "key-down", 502, msgUnavailable},
		{"not allowlisted", base, "key-mallory", 403, "@mallory isn&#39;t on this server&#39;s allowlist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, body := e.post(t, tc.origin, url.Values{"q": {q}, "decision": {"connect"}, "key": {tc.key}})
			if res.StatusCode != tc.status || res.Header.Get("Location") != "" {
				t.Fatalf("status %d, location %q", res.StatusCode, res.Header.Get("Location"))
			}
			if !strings.Contains(body, strings.ReplaceAll(tc.want, "'", "&#39;")) {
				t.Fatalf("body lacks %q: %s", tc.want, body)
			}
			if k := strings.TrimSpace(tc.key); k != "" && strings.Contains(body, k) {
				t.Fatal("the page echoed the key")
			}
		})
	}
}

// An admin connects without being on the allowlist; someone else turned
// away is logged with their ID, for adding them by hand.
func TestConnectAdminAndRefusalLog(t *testing.T) {
	e := newEnv(t, nil)
	q := authorizeQuery(e.register(t))
	if res, body := e.post(t, base, url.Values{"q": {q}, "decision": {"connect"}, "key": {"key-mallory"}}); res.StatusCode != 403 {
		t.Fatalf("mallory: %d %s", res.StatusCode, body)
	}
	if !strings.Contains(e.logs.String(), `msg="connect: not on the allowlist" username=mallory user_id=9`) {
		t.Fatalf("log: %s", e.logs)
	}
	e.oauth.Admins = []string{"9"}
	if res, body := e.post(t, base, url.Values{"q": {q}, "decision": {"connect"}, "key": {"key-mallory"}}); res.StatusCode != http.StatusFound {
		t.Fatalf("mallory as admin: %d %s", res.StatusCode, body)
	}
}

func TestConnectCancelAndBadRequests(t *testing.T) {
	e := newEnv(t, nil)
	clientID := e.register(t)
	res, _ := e.post(t, base, url.Values{"q": {authorizeQuery(clientID)}, "decision": {"cancel"}})
	if loc, _ := url.Parse(res.Header.Get("Location")); res.StatusCode != 302 || loc.Query().Get("error") != "access_denied" {
		t.Fatalf("cancel: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// A request the client must hear about goes back to it.
	q, _ := url.ParseQuery(authorizeQuery(clientID))
	q.Del("code_challenge")
	res, err := noRedirects.Get(e.srv.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if loc, _ := url.Parse(res.Header.Get("Location")); res.StatusCode != 302 || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("no PKCE: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// One it can't be sent back to is shown.
	res, err = http.Get(e.srv.URL + "/oauth/authorize?" + authorizeQuery("no-such-client"))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 400 {
		t.Fatalf("unknown client: %d", res.StatusCode)
	}
}

func TestHomeHealthAndOriginVerify(t *testing.T) {
	e := newEnv(t, nil)
	for path, want := range map[string]string{"/": "https://dustjacket.test/mcp", "/healthz": "ok", "/static/style.css": "--accent"} {
		res, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(b), want) {
			t.Errorf("%s: %d %s", path, res.StatusCode, b)
		}
	}

	s := &Server{OAuth: e.oauth, MCP: http.NotFoundHandler(), BaseURL: base, OriginVerify: "sekrit"}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	res, _ := http.Get(srv.URL + "/healthz")
	if res.StatusCode != 403 {
		t.Fatalf("without X-Origin-Verify: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/healthz", nil)
	req.Header.Set("X-Origin-Verify", "sekrit")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 200 {
		t.Fatalf("with X-Origin-Verify: %d", res.StatusCode)
	}
}

func TestAccessLogCarriesHandlerFields(t *testing.T) {
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqlog.From(r.Context()).Add("tool", "hardcover_query", "hc_status", 200)
		w.WriteHeader(202)
	})
	e := newEnv(t, mcp)
	res, err := http.Post(e.srv.URL+"/mcp?code=secret-code", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	line := e.logs.String()
	for _, want := range []string{"msg=request", "path=/mcp", "status=202", "tool=hardcover_query", "hc_status=200"} {
		if !strings.Contains(line, want) {
			t.Errorf("log lacks %s: %s", want, line)
		}
	}
	if strings.Contains(line, "secret-code") {
		t.Fatalf("the query reached the log: %s", line)
	}
}

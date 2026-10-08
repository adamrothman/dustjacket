package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// TestEndToEnd walks what claude.ai does: find the authorization server,
// register, send the person to the connect page, exchange the code, then
// call a tool, which reaches Hardcover with the person's key.
func TestEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var seenAuth []string
	hc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		io.WriteString(w, `{"data":{"me":[{"id":7,"username":"adam"}]}}`)
	}))
	defer hc.Close()
	t.Setenv("DUSTJACKET_BASE_URL", "https://dustjacket.test")
	t.Setenv("DUSTJACKET_ADMIN_IDS", "7")
	t.Setenv("DUSTJACKET_HARDCOVER_URL", hc.URL)
	handler, _, err := build(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	getJSON := func(res *http.Response, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("HTTP %d: %v", res.StatusCode, err)
		}
		return out
	}

	// 1. An unauthenticated call points at the resource metadata.
	res, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), "https://dustjacket.test/.well-known/oauth-protected-resource") {
		t.Fatalf("challenge: %d %v", res.StatusCode, res.Header)
	}
	meta := getJSON(http.Get(srv.URL + "/.well-known/oauth-protected-resource"))
	if meta["authorization_servers"].([]any)[0] != "https://dustjacket.test" {
		t.Fatalf("resource metadata: %v", meta)
	}

	// 2. Register.
	reg := getJSON(http.Post(srv.URL+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"token_endpoint_auth_method":"none"}`)))
	clientID := reg["client_id"].(string)

	// 3. The connect page, then the key.
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "state": {"s"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {"https://dustjacket.test/mcp"}}.Encode()
	if res, err := http.Get(srv.URL + "/oauth/authorize?" + q); err != nil || res.StatusCode != 200 {
		t.Fatalf("connect page: %v %v", res, err)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/oauth/authorize", strings.NewReader(url.Values{"q": {q}, "decision": {"connect"}, "key": {"key-adam"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://dustjacket.test")
	res, err = browser.Do(req)
	if err != nil || res.StatusCode != 302 {
		t.Fatalf("connect: %v %v", res, err)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))

	// 4. Exchange the code.
	tok := getJSON(http.PostForm(srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {loc.Query().Get("code")}, "code_verifier": {verifier}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}}))
	access, _ := tok["access_token"].(string)
	if access == "" {
		t.Fatalf("token: %v", tok)
	}

	// 5. Use the tools.
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-ish", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{access, http.DefaultTransport}, Timeout: 10 * time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// The admin, the one person a fresh table lets in, gets the allowlist
	// tools as well.
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 6 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	r, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "hardcover_query", Arguments: map[string]any{"query": "{ me { id username } }"}})
	if err != nil || r.IsError || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, `"username":"adam"`) {
		t.Fatalf("query: %v %+v", err, r)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, a := range seenAuth {
		if a != "Bearer key-adam" {
			t.Fatalf("Hardcover saw %q", a)
		}
	}
	if len(seenAuth) != 2 { // `me` at connect, then the tool call
		t.Fatalf("Hardcover calls: %d", len(seenAuth))
	}
}

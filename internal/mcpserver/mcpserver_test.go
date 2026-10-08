package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/reqlog"
	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

// fakeHardcover records what it was sent and answers with the next reply.
type fakeHardcover struct {
	mu     sync.Mutex
	auth   string
	body   map[string]any
	status int
	header map[string]string
	reply  string
}

func (f *fakeHardcover) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	f.body = nil
	_ = json.NewDecoder(r.Body).Decode(&f.body)
	for k, v := range f.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(f.status)
	io.WriteString(w, f.reply)
}

func (f *fakeHardcover) set(status int, header map[string]string, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.header, f.reply, f.body = status, header, reply, nil
}

func (f *fakeHardcover) seen() (string, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth, f.body
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

type env struct {
	url   string
	token string
	hc    *fakeHardcover
	store *store.Memory
	logs  *[][]any
}

// newEnv serves the handler for a connected user, 7 "adam", on the
// allowlist, whose Hardcover key is "key-adam", recording each request's
// reqlog fields as the access log would.
func newEnv(t *testing.T) *env {
	return newEnvFor(t, false)
}

// newAdminEnv is newEnv with adam an admin rather than on the allowlist.
func newAdminEnv(t *testing.T) *env {
	return newEnvFor(t, true)
}

func newEnvFor(t *testing.T, admin bool) *env {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	sl := sealer.NewLocal()
	oa := &oauth.Server{Store: st, Sealer: sl, Issuer: "https://dustjacket.test"}
	if admin {
		oa.Admins = []string{"7", "99"}
	} else if _, err := oa.Allow(ctx, oauth.AllowedUser{ID: "7", Username: "adam"}); err != nil {
		t.Fatal(err)
	}
	sealed, err := sl.Seal(ctx, "7", []byte("key-adam"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, oauth.User{ID: "7", Username: "adam", KeyCiphertext: sealed.Ciphertext, KeySealedBy: sealed.KeyID}); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, oauth.Grant{ID: "g1", ClientID: "c1", UserID: "7"}); err != nil {
		t.Fatal(err)
	}
	tok, err := oa.MintAccessToken(ctx, "g1")
	if err != nil {
		t.Fatal(err)
	}
	hc := &fakeHardcover{status: 200, reply: `{"data":{}}`}
	hcSrv := httptest.NewServer(hc)
	t.Cleanup(hcSrv.Close)

	h := &Handler{OAuth: oa, Hardcover: &hardcover.Client{URL: hcSrv.URL}, Version: "test"}
	var mu sync.Mutex
	logs := &[][]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, f := reqlog.With(r.Context())
		h.ServeHTTP(w, r.WithContext(ctx))
		mu.Lock()
		*logs = append(*logs, f.Attrs())
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return &env{url: srv.URL, token: tok, hc: hc, store: st, logs: logs}
}

func (e *env) session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	hc := &http.Client{Transport: bearer{e.token, http.DefaultTransport}}
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.url, HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// call returns a tool's text and whether it was an error.
func call(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	r, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r.Content[0].(*mcp.TextContent).Text, r.IsError
}

func TestUnauthenticated(t *testing.T) {
	e := newEnv(t)
	res, err := http.Post(e.url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
}

func TestToolList(t *testing.T) {
	sess := newEnv(t).session(t)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.ToolAnnotations{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool.Annotations
	}
	if len(got) != 3 {
		t.Fatalf("tools: %v", got)
	}
	if a := got["hardcover_query"]; a == nil || !a.ReadOnlyHint {
		t.Errorf("hardcover_query: %+v", a)
	}
	if a := got["hardcover_mutate"]; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Errorf("hardcover_mutate: %+v", a)
	}
	if a := got["hardcover_docs"]; a == nil || !a.ReadOnlyHint {
		t.Errorf("hardcover_docs: %+v", a)
	}
}

func TestQueryPassesThrough(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t)
	// Titles and reviews are not ASCII, and carry &, < and >; they pass
	// through byte for byte, not as \u0026 and \u003c.
	e.hc.set(200, map[string]string{"RateLimit": `"Free";r=58;t=60`}, `{"data":{"me":[{"id":7,"username":"adam","bio":"Brontë ✓ 読書 — Eleanor & Park <p>"}]}}`)
	text, isErr := call(t, sess, "hardcover_query", map[string]any{"query": "query Me($n: Int, $q: String) { me(limit: $n) { id username bio } }", "variables": map[string]any{"n": 1, "q": "Brontë"}, "operation_name": "Me"})
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		RateLimit string          `json:"rate_limit"`
		Response  json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `Eleanor & Park <p>`) || out.RateLimit != `"Free";r=58;t=60` || string(out.Response) != `{"data":{"me":[{"id":7,"username":"adam","bio":"Brontë ✓ 読書 — Eleanor & Park <p>"}]}}` {
		t.Fatalf("result: %s", text)
	}
	auth, body := e.hc.seen()
	if auth != "Bearer key-adam" || body["operationName"] != "Me" || body["variables"].(map[string]any)["n"] != float64(1) || body["variables"].(map[string]any)["q"] != "Brontë" {
		t.Fatalf("sent: %s %v", auth, body)
	}
	found := false
	for _, attrs := range *e.logs {
		if slices.Contains(attrs, "hardcover_query") && slices.Contains(attrs, any("me")) && slices.Contains(attrs, any(200)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("log fields: %v", *e.logs)
	}
}

func TestOperationKindIsEnforced(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t)
	for _, tc := range []struct {
		tool, query, want string
	}{
		{"hardcover_query", "mutation { delete_user_book(id: 1) { id } }", "hardcover_query runs queries only"},
		{"hardcover_query", "query A { me { id } } mutation B { delete_user_book(id: 1) { id } }", "hardcover_query runs queries only"},
		{"hardcover_mutate", "{ me { id } }", "hardcover_mutate runs mutations only"},
		{"hardcover_query", "subscription { me { id } }", "subscriptions aren't supported"},
		{"hardcover_query", "query { me { id }", "doesn't parse"},
		{"hardcover_query", "fragment F on users { id }", "no operation"},
	} {
		text, isErr := call(t, sess, tc.tool, map[string]any{"query": tc.query})
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s %q: %v %s", tc.tool, tc.query, isErr, text)
		}
	}
	if _, body := e.hc.seen(); body != nil {
		t.Fatalf("a rejected document reached Hardcover: %v", body)
	}
	e.hc.set(200, nil, `{"data":{"insert_list":{"id":3}}}`)
	if text, isErr := call(t, sess, "hardcover_mutate", map[string]any{"query": `mutation { insert_list(object: {name: "x"}) { id } }`}); isErr || !strings.Contains(text, `"id":3`) {
		t.Fatalf("mutation: %v %s", isErr, text)
	}
}

func TestHardcoverFailures(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t)
	for _, tc := range []struct {
		status int
		header map[string]string
		reply  string
		want   string
	}{
		{200, nil, `{"errors":[{"message":"field 'nope' not found"}]}`, "field 'nope' not found"},
		{401, nil, `{"error":"invalid_token"}`, "disconnect and reconnect Dustjacket"},
		{403, nil, `{"error":"insufficient_scope","scope":"read:lists"}`, "lacks the `read:lists` scope. Create a new key at " + hardcover.KeyURL},
		{403, nil, `{"error":"top_level_limit_exceeded"}`, "at most 5 top-level fields"},
		{403, nil, `{"error":"unsupported_operation"}`, `Hardcover refused the request: {"error":"unsupported_operation"}`},
		{429, map[string]string{"Retry-After": "7"}, `{}`, "retry after 7 seconds"},
		{503, nil, ``, "safe to retry"},
		{200, nil, `{"data":"` + strings.Repeat("x", 150<<10) + `"}`, "over the 100 KB limit"},
	} {
		e.hc.set(tc.status, tc.header, tc.reply)
		text, isErr := call(t, sess, "hardcover_query", map[string]any{"query": "{ me { id } }"})
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("HTTP %d %.40s: %v %.300s", tc.status, tc.reply, isErr, text)
		}
	}
}

// A mutation that fails in transit may still have happened, so Claude
// must check before trying again rather than repeat it; one whose result
// was too big did happen.
func TestMutationFailuresDontInviteARetry(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t)
	mutation := map[string]any{"query": `mutation { insert_list(object: {name: "x"}) { id } }`}
	for _, tc := range []struct {
		status  int
		header  map[string]string
		reply   string
		want    string
		wantNot string
	}{
		{503, nil, ``, "may or may not have been made. Check with hardcover_query before trying it again", "safe to retry"},
		{408, nil, ``, "may or may not have been made", "safe to retry"},
		{200, nil, `{"data":"` + strings.Repeat("x", 150<<10) + `"}`, "The change was made, but its response was 150 KB", "Ask for fewer fields or add a `limit`"},
		{200, nil, `{"data":"` + strings.Repeat("x", 5<<20) + `"}`, "The change was made, but its response was over 4 MB", "4096 KB"},
		// Refused outright: nothing happened, and waiting is the fix.
		{429, map[string]string{"Retry-After": "3"}, `{}`, "retry after 3 seconds", "may or may not"},
	} {
		e.hc.set(tc.status, tc.header, tc.reply)
		text, isErr := call(t, sess, "hardcover_mutate", mutation)
		if !isErr || !strings.Contains(text, tc.want) || strings.Contains(text, tc.wantNot) {
			t.Errorf("HTTP %d: %v %.300s", tc.status, isErr, text)
		}
	}
	// Reads are still safe to retry.
	e.hc.set(503, nil, ``)
	if text, _ := call(t, sess, "hardcover_query", map[string]any{"query": "{ me { id } }"}); !strings.Contains(text, "safe to retry") {
		t.Errorf("query 503: %s", text)
	}
}

func TestDocs(t *testing.T) {
	sess := newEnv(t).session(t)
	for _, tc := range []struct {
		args    map[string]any
		want    string
		wantErr bool
	}{
		{map[string]any{}, "# Dustjacket", false},
		{map[string]any{"topic": "library"}, "# Library", false},
		{map[string]any{"name": "user_books"}, "type user_books {", false},
		{map[string]any{"topic": "nope"}, "the topics are library", true},
		{map[string]any{"name": "UserBooks"}, "did you mean", true},
		{map[string]any{"topic": "library", "name": "user_books"}, "either topic or name", true},
	} {
		text, isErr := call(t, sess, "hardcover_docs", tc.args)
		if isErr != tc.wantErr || !strings.Contains(text, tc.want) {
			t.Errorf("%v: %v %.200s", tc.args, isErr, text)
		}
	}
}

// A client on the 2026-07-28 protocol follows discovery with a
// subscriptions/listen stream for list-changed notifications. A buffered
// function URL cannot hold a stream open, and the tools never change under
// a running server, so the listen must be acknowledged and end at once
// (pickem's decision 30).
func TestSubscriptionsListenEndsAtOnce(t *testing.T) {
	e := newEnv(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"subscriptions/listen","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test","version":"0"},` +
		`"io.modelcontextprotocol/clientCapabilities":{}},` +
		`"notifications":{"toolsListChanged":true}}}`
	req, err := http.NewRequest("POST", e.url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "subscriptions/listen")
	res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("listen did not end: %v (read so far: %s)", err, got)
	}
	if res.StatusCode != 200 || !strings.Contains(string(got), `"result"`) || strings.Contains(string(got), "toolsListChanged") {
		t.Fatalf("listen: %d %s", res.StatusCode, got)
	}
}

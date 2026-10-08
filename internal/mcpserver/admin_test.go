package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/store"
)

// Only admins see the allowlist tools, and anyone else can't call them.
func TestAdminToolsAreForAdmins(t *testing.T) {
	sess := newEnv(t).session(t)
	if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "allowlist_add", Arguments: map[string]any{"username": "mallory"}}); err == nil {
		t.Fatal("a non-admin called allowlist_add")
	}

	res, err := newAdminEnv(t).session(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.ToolAnnotations{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool.Annotations
	}
	if len(got) != 6 {
		t.Fatalf("admin tools: %v", got)
	}
	if a := got["allowlist_list"]; a == nil || !a.ReadOnlyHint {
		t.Errorf("allowlist_list: %+v", a)
	}
	// Changes aren't read-only, so claude.ai asks before making them.
	if a := got["allowlist_add"]; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
		t.Errorf("allowlist_add: %+v", a)
	}
	if a := got["allowlist_remove"]; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Errorf("allowlist_remove: %+v", a)
	}
}

func TestAllowlistTools(t *testing.T) {
	e := newAdminEnv(t)
	sess := e.session(t)
	list := func() (admins, users []listedUser) {
		t.Helper()
		text, isErr := call(t, sess, "allowlist_list", nil)
		var out struct{ Admins, Users []listedUser }
		if err := json.Unmarshal([]byte(text), &out); isErr || err != nil {
			t.Fatalf("list: %v %v %s", isErr, err, text)
		}
		return out.Admins, out.Users
	}
	admins, users := list()
	if !slices.Equal(admins, []listedUser{{Username: "adam", UserID: "7"}, {UserID: "99"}}) || len(users) != 0 {
		t.Fatalf("list before: %+v %+v", admins, users)
	}

	// Added by the username Hardcover has, looked up with the admin's key.
	e.hc.set(200, nil, `{"data":{"users":[{"id":8,"username":"sister"}]}}`)
	if text, isErr := call(t, sess, "allowlist_add", map[string]any{"username": " @Sister"}); isErr || !strings.Contains(text, "Added @sister (Hardcover user 8)") {
		t.Fatalf("add: %v %s", isErr, text)
	}
	auth, body := e.hc.seen()
	if auth != "Bearer key-adam" || body["variables"].(map[string]any)["username"] != "Sister" {
		t.Fatalf("lookup: %s %v", auth, body)
	}
	if text, isErr := call(t, sess, "allowlist_add", map[string]any{"username": "sister"}); isErr || !strings.Contains(text, "on the allowlist already") {
		t.Fatalf("add again: %v %s", isErr, text)
	}
	e.hc.set(200, nil, `{"data":{"users":[{"id":99,"username":"boss"}]}}`)
	if text, isErr := call(t, sess, "allowlist_add", map[string]any{"username": "boss"}); isErr || !strings.Contains(text, "is an admin") {
		t.Fatalf("add an admin: %v %s", isErr, text)
	}
	e.hc.set(200, nil, `{"data":{"users":[]}}`)
	if text, isErr := call(t, sess, "allowlist_add", map[string]any{"username": "ghost"}); !isErr || !strings.Contains(text, "no user @ghost") {
		t.Fatalf("add nobody: %v %s", isErr, text)
	}
	e.hc.set(401, nil, `{"error":"invalid_token"}`)
	if text, isErr := call(t, sess, "allowlist_add", map[string]any{"username": "nick"}); !isErr || !strings.Contains(text, "disconnect and reconnect") {
		t.Fatalf("add with a dead key: %v %s", isErr, text)
	}
	if _, users := list(); len(users) != 1 || users[0].Username != "sister" || users[0].UserID != "8" || users[0].AddedBy != "adam" || users[0].AddedAt.IsZero() {
		t.Fatalf("list after adding: %+v", users)
	}

	// Removing someone deletes their stored key.
	if err := e.store.Put(context.Background(), oauth.User{ID: "8", Username: "sister"}); err != nil {
		t.Fatal(err)
	}
	if text, isErr := call(t, sess, "allowlist_remove", map[string]any{"username": "@SISTER"}); isErr || !strings.Contains(text, "Removed @sister (Hardcover user 8)") {
		t.Fatalf("remove: %v %s", isErr, text)
	}
	var u oauth.User
	if err := e.store.Get(context.Background(), store.Key{PK: "USER#8", SK: "META"}, &u); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("user record after removal: %v", err)
	}
	if text, isErr := call(t, sess, "allowlist_remove", map[string]any{"username": "sister"}); !isErr || !strings.Contains(text, "Nobody on the allowlist has the username @sister") {
		t.Fatalf("remove again: %v %s", isErr, text)
	}
	if text, isErr := call(t, sess, "allowlist_remove", map[string]any{"username": "Adam"}); !isErr || !strings.Contains(text, "You're an admin") {
		t.Fatalf("remove myself: %v %s", isErr, text)
	}
	if _, users := list(); len(users) != 0 {
		t.Fatalf("list after removing: %+v", users)
	}

	// The access log says who changed what.
	var changes [][]any
	for _, attrs := range *e.logs {
		if slices.Contains(attrs, "result") {
			changes = append(changes, attrs)
		}
	}
	want := [][]any{
		{"user", "adam", "tool", "allowlist_add", "target", "sister", "target_id", "8", "result", "added"},
		{"user", "adam", "tool", "allowlist_add", "target", "sister", "target_id", "8", "result", "already"},
		{"user", "adam", "tool", "allowlist_add", "target", "boss", "target_id", "99", "result", "admin"},
		{"user", "adam", "tool", "allowlist_add", "result", "no_such_user"},
		{"user", "adam", "tool", "allowlist_remove", "target", "sister", "target_id", "8", "result", "removed"},
		{"user", "adam", "tool", "allowlist_remove", "result", "not_listed"},
		{"user", "adam", "tool", "allowlist_remove", "target", "adam", "target_id", "7", "result", "admin"},
	}
	if !slices.EqualFunc(changes, want, slices.Equal) {
		t.Fatalf("log fields:\n%v\nwant\n%v", changes, want)
	}
}

package mcpserver

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
)

// The admin tools manage the allowlist (docs/decisions.md 23). Only admins
// are given them, and each asks Claude to act only when the person names
// who: Hardcover text Claude reads, such as other people's reviews, could
// ask for a change too.

type UsernameInput struct {
	Username string `json:"username" jsonschema:"a Hardcover username, with or without the @"`
}

func (t *tools) addAdminTools(s *mcp.Server) {
	yes, no := true, false
	mcp.AddTool(s, &mcp.Tool{
		Name:        "allowlist_list",
		Description: "List who may connect to Dustjacket: the admins, who are set in Dustjacket's configuration, and everyone on the allowlist, with when they were added and by whom.",
		Annotations: &mcp.ToolAnnotations{Title: "List Dustjacket's users", ReadOnlyHint: true, OpenWorldHint: &no},
	}, t.allowlistList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "allowlist_add",
		Description: "Let someone connect to Dustjacket: looks their username up on Hardcover and adds them to the allowlist. Only when the person you are talking to asks for it and names who.",
		Annotations: &mcp.ToolAnnotations{Title: "Add a Dustjacket user", DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &yes},
	}, t.allowlistAdd)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "allowlist_remove",
		Description: "Stop someone using Dustjacket: takes them off the allowlist and deletes their stored Hardcover key, which ends all of their connections at once. Only when the person you are talking to asks for it and names who.",
		Annotations: &mcp.ToolAnnotations{Title: "Remove a Dustjacket user", DestructiveHint: &yes, IdempotentHint: true, OpenWorldHint: &no},
	}, t.allowlistRemove)
}

type listedUser struct {
	Username string    `json:"username,omitempty"`
	UserID   string    `json:"user_id"`
	AddedAt  time.Time `json:"added_at,omitzero"`
	AddedBy  string    `json:"added_by,omitempty"`
}

func (t *tools) allowlistList(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	t.log.Add("tool", "allowlist_list")
	l, err := t.oa.Allowlist(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := struct {
		Admins []listedUser `json:"admins"`
		Users  []listedUser `json:"users"`
	}{Admins: []listedUser{}, Users: []listedUser{}}
	// Only the caller's username is at hand for an admin.
	for _, id := range t.oa.Admins {
		a := listedUser{UserID: id}
		if id == t.caller.UserID {
			a.Username = t.caller.Username
		}
		out.Admins = append(out.Admins, a)
	}
	for _, u := range l.Users {
		out.Users = append(out.Users, listedUser{Username: u.Username, UserID: u.ID, AddedAt: u.AddedAt, AddedBy: u.AddedBy})
	}
	slices.SortFunc(out.Users, func(a, b listedUser) int {
		return cmp.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username))
	})
	return jsonResult(out)
}

func (t *tools) allowlistAdd(ctx context.Context, _ *mcp.CallToolRequest, in UsernameInput) (*mcp.CallToolResult, any, error) {
	t.log.Add("tool", "allowlist_add")
	name := strings.TrimPrefix(strings.TrimSpace(in.Username), "@")
	if name == "" {
		return nil, nil, errors.New("give the Hardcover username of the person to add")
	}
	u, err := t.hc.UserByUsername(ctx, t.key, name)
	switch {
	case errors.Is(err, hardcover.ErrNoSuchUser):
		t.log.Add("result", "no_such_user")
		return nil, nil, fmt.Errorf("Hardcover has no user @%s that your key can see. Check the spelling with the person. If it's right, their account may be hidden from you: have them try to connect, and the refusal in Dustjacket's log names their user ID (docs/runbook.md).", name)
	case err != nil:
		var he *hardcover.Error
		if errors.As(err, &he) {
			return nil, nil, errors.New(describe(err, ast.Query))
		}
		return nil, nil, fmt.Errorf("couldn't look up @%s on Hardcover: %v", name, err)
	}
	id := strconv.Itoa(u.ID)
	t.log.Add("target", u.Username, "target_id", id)
	added, err := t.oa.Allow(ctx, oauth.AllowedUser{ID: id, Username: u.Username, AddedBy: t.caller.Username})
	var text string
	switch {
	case errors.Is(err, oauth.ErrAdmin):
		t.log.Add("result", "admin")
		text = fmt.Sprintf("@%s (Hardcover user %s) is an admin, so they can connect already. Admins are set in Dustjacket's configuration, not on the allowlist.", u.Username, id)
	case err != nil:
		return nil, nil, err
	case !added:
		t.log.Add("result", "already")
		text = fmt.Sprintf("@%s (Hardcover user %s) is on the allowlist already.", u.Username, id)
	default:
		t.log.Add("result", "added")
		text = fmt.Sprintf("Added @%s (Hardcover user %s) to the allowlist. They can connect now, with a Hardcover key of their own.", u.Username, id)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

func (t *tools) allowlistRemove(ctx context.Context, _ *mcp.CallToolRequest, in UsernameInput) (*mcp.CallToolResult, any, error) {
	t.log.Add("tool", "allowlist_remove")
	name := strings.TrimPrefix(strings.TrimSpace(in.Username), "@")
	if name == "" {
		return nil, nil, errors.New("give the Hardcover username of the person to remove")
	}
	removed, err := t.oa.Disallow(ctx, name)
	switch {
	case err != nil:
		return nil, nil, err
	case removed == nil && strings.EqualFold(name, t.caller.Username):
		t.log.Add("target", t.caller.Username, "target_id", t.caller.UserID, "result", "admin")
		return nil, nil, errors.New("You're an admin. Admins are set in Dustjacket's configuration, not on the allowlist, so they can't be removed here.")
	case removed == nil:
		t.log.Add("result", "not_listed")
		return nil, nil, fmt.Errorf("Nobody on the allowlist has the username @%s. allowlist_list shows who is on it, under the username they had when they were added.", name)
	}
	t.log.Add("target", removed.Username, "target_id", removed.ID, "result", "removed")
	text := fmt.Sprintf("Removed @%s (Hardcover user %s) from the allowlist and deleted their stored Hardcover key. Their connections stopped working; if they are added back, they connect again with a key.", removed.Username, removed.ID)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

// jsonResult is v as the tool's text, indented for reading and, like the
// GraphQL tools' results, without HTML escaping.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimSuffix(buf.String(), "\n")}}}, nil, nil
}

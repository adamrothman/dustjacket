// Package mcpserver is /mcp: a Streamable HTTP MCP server authenticated by
// Dustjacket's own OAuth tokens, whose three tools run Hardcover's GraphQL
// API as the person the token was granted for, and whose admins also get
// three for the allowlist. Stateless with JSON responses, so it works
// behind a buffered Lambda function URL.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/reference"
	"github.com/adamrothman/dustjacket/internal/reqlog"
)

// maxResult is the most a GraphQL tool returns, serialized: past it,
// Claude is asked for a narrower query (docs/decisions.md 10).
const maxResult = 100 << 10

const instructions = `Dustjacket gives you the Hardcover API (GraphQL) as the connected user. Call hardcover_docs with no arguments before your first query in a conversation, and read the relevant guide before any mutation. Use hardcover_query to read and hardcover_mutate to write. Reading statuses: 1 Want to Read, 2 Currently Reading, 3 Read, 4 Paused, 5 Did Not Finish, 6 Ignored. Names match only through search (_like, _ilike and regex operators are disabled). Each request may have at most 5 top-level fields, or 1 search; each top-level field counts against 60 requests a minute.`

type Handler struct {
	OAuth     *oauth.Server
	Hardcover *hardcover.Client
	Version   string
	Log       *slog.Logger
}

// ServeHTTP authenticates the bearer token, then hands the request to an
// MCP server bound to that caller.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caller, err := h.OAuth.Authenticate(r.Context(), r)
	if errors.Is(err, oauth.ErrUnauthorized) {
		h.OAuth.Challenge(w)
		return
	}
	if err != nil {
		h.log().Error("authenticate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	fields := reqlog.From(r.Context())
	fields.Add("user", caller.Username)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return h.server(caller, fields)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	handler.ServeHTTP(w, r)
}

func (h *Handler) log() *slog.Logger {
	if h.Log == nil {
		return slog.Default()
	}
	return h.Log
}

func (h *Handler) server(c *oauth.Caller, fields *reqlog.Fields) *mcp.Server {
	// Tools without listChanged: the list is fixed for each caller for
	// the life of the binary, and a client that subscribes to changes
	// anyway would otherwise hold its request open until Lambda times it
	// out.
	s := mcp.NewServer(&mcp.Implementation{Name: "dustjacket", Title: "Dustjacket", Version: h.Version}, &mcp.ServerOptions{
		Instructions: instructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	t := &tools{hc: h.Hardcover, oa: h.OAuth, caller: c, key: c.Key, log: fields}
	yes, no := true, false
	mcp.AddTool(s, &mcp.Tool{
		Name:        "hardcover_query",
		Description: "Run a GraphQL query against Hardcover as the connected person. Reads only; hardcover_mutate changes things. Call hardcover_docs first for ids, patterns and limits. Returns Hardcover's JSON response and the rate limit remaining.",
		Annotations: &mcp.ToolAnnotations{Title: "Query Hardcover", ReadOnlyHint: true, OpenWorldHint: &yes},
	}, t.query)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "hardcover_mutate",
		Description: "Run a GraphQL mutation against Hardcover as the connected person: their library, reads and progress, ratings and reviews, lists, goals, journal. Read the topic's hardcover_docs guide first, and tell the person what will change.",
		Annotations: &mcp.ToolAnnotations{Title: "Change Hardcover", DestructiveHint: &yes, OpenWorldHint: &yes},
	}, t.mutate)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "hardcover_docs",
		Description: "Dustjacket's reference for the Hardcover API. No arguments: the overview; start here. topic: a guide (" + strings.Join(reference.Topics, ", ") + "). name: a type's or root field's definition from the schema, e.g. user_books, UserBookUpdateInput, mutation_root.update_user_book.",
		Annotations: &mcp.ToolAnnotations{Title: "Hardcover API docs", ReadOnlyHint: true, OpenWorldHint: &no},
	}, t.docs)
	if c.Admin {
		t.addAdminTools(s)
	}
	return s
}

type GraphQLInput struct {
	Query         string         `json:"query" jsonschema:"a GraphQL document"`
	Variables     map[string]any `json:"variables,omitempty" jsonschema:"values for the document's variables"`
	OperationName string         `json:"operation_name,omitempty" jsonschema:"which operation to run, when the document has several"`
}

type DocsInput struct {
	Topic string `json:"topic,omitempty" jsonschema:"a guide to read"`
	Name  string `json:"name,omitempty" jsonschema:"a type or root field to define, e.g. user_books or mutation_root.update_user_book"`
}

type tools struct {
	hc     *hardcover.Client
	oa     *oauth.Server
	caller *oauth.Caller
	key    string
	log    *reqlog.Fields
}

func (t *tools) query(ctx context.Context, _ *mcp.CallToolRequest, in GraphQLInput) (*mcp.CallToolResult, any, error) {
	return t.run(ctx, "hardcover_query", ast.Query, in)
}

func (t *tools) mutate(ctx context.Context, _ *mcp.CallToolRequest, in GraphQLInput) (*mcp.CallToolResult, any, error) {
	return t.run(ctx, "hardcover_mutate", ast.Mutation, in)
}

func (t *tools) run(ctx context.Context, tool string, want ast.Operation, in GraphQLInput) (*mcp.CallToolResult, any, error) {
	t.log.Add("tool", tool)
	fields, err := check(in.Query, want)
	if err != nil {
		return nil, nil, err
	}
	t.log.Add("op", string(want), "fields", strings.Join(fields, ","))
	start := time.Now()
	res, err := t.hc.Do(ctx, t.key, hardcover.Request{Query: in.Query, Variables: in.Variables, OperationName: in.OperationName})
	status := http.StatusOK
	var he *hardcover.Error
	switch {
	case errors.As(err, &he):
		status = he.Status
	case err != nil:
		status = 0
	}
	t.log.Add("hc_status", status, "hc_ms", time.Since(start).Milliseconds())
	if err != nil {
		return nil, nil, errors.New(describe(err, want))
	}
	// Not json.Marshal: it would escape &, < and > in titles and reviews.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		RateLimit string          `json:"rate_limit"`
		Response  json.RawMessage `json:"response"`
	}{res.RateLimit, res.Body}); err != nil {
		return nil, nil, err
	}
	text := strings.TrimSuffix(buf.String(), "\n")
	if len(text) > maxResult {
		return nil, nil, errors.New(tooLarge(fmt.Sprintf("%d KB", len(text)>>10), want))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: res.HasErrors}, nil, nil
}

// check parses the document and requires every operation in it to be of
// the tool's kind, returning the top-level field names for the log.
func check(doc string, want ast.Operation) ([]string, error) {
	d, err := parser.ParseQuery(&ast.Source{Input: doc})
	if err != nil {
		return nil, fmt.Errorf("the GraphQL doesn't parse: %v", err)
	}
	if len(d.Operations) == 0 {
		return nil, errors.New("the document has no operation to run")
	}
	var fields []string
	for _, op := range d.Operations {
		switch {
		case op.Operation == ast.Subscription:
			return nil, errors.New("subscriptions aren't supported")
		case op.Operation != want && want == ast.Query:
			return nil, errors.New("hardcover_query runs queries only; use hardcover_mutate for mutations")
		case op.Operation != want:
			return nil, errors.New("hardcover_mutate runs mutations only; use hardcover_query to read")
		}
		for _, sel := range op.SelectionSet {
			if f, ok := sel.(*ast.Field); ok {
				fields = append(fields, f.Name)
			}
		}
	}
	return fields, nil
}

const reconnect = "Hardcover rejected your API key (expired or revoked). In Claude's settings, disconnect and reconnect Dustjacket to paste a new one."

// tooLarge is a result over the limit. A mutation's already happened, so
// it must not be run again to get a smaller answer.
func tooLarge(size string, op ast.Operation) string {
	if op == ast.Mutation {
		return "The change was made, but its response was " + size + ", over the 100 KB limit. Don't run it again; ask for fewer fields in the result next time."
	}
	return "The response was " + size + ", over the 100 KB limit. Ask for fewer fields or add a `limit`."
}

// describe turns a failed Hardcover request into what Claude should know
// to act on it. A mutation that got no answer may still have been applied
// (Hardcover allows 30 s; Dustjacket waits 20), so it isn't safe to repeat.
func describe(err error, op ast.Operation) string {
	var he *hardcover.Error
	if !errors.As(err, &he) {
		return "The request to Hardcover failed: " + err.Error()
	}
	switch {
	case he.Code == hardcover.CodeTooLarge:
		return tooLarge("over 4 MB", op)
	case he.Status == http.StatusUnauthorized:
		return reconnect
	case he.Status == http.StatusForbidden && he.Code == "insufficient_scope":
		return fmt.Sprintf("Your Hardcover key lacks the `%s` scope. Create a new key at %s, then disconnect and reconnect Dustjacket in Claude's settings to use it.", he.Scope, hardcover.KeyURL)
	case he.Status == http.StatusForbidden && he.Code == "top_level_limit_exceeded":
		return "Hardcover allows at most 5 top-level fields per request, or 1 `search`. Split the request."
	case he.Status == http.StatusForbidden:
		return "Hardcover refused the request: " + he.Body
	case he.Status == http.StatusTooManyRequests:
		wait := he.RetryAfter
		if wait == "" {
			wait = "a few"
		}
		return "Rate limited by Hardcover; retry after " + wait + " seconds."
	case he.Retryable() && op == ast.Mutation:
		return "Hardcover timed out or is unavailable, so the change may or may not have been made. Check with hardcover_query before trying it again."
	case he.Retryable():
		return "Hardcover timed out or is unavailable; this is safe to retry."
	}
	return fmt.Sprintf("Hardcover returned HTTP %d: %s", he.Status, he.Body)
}

func (t *tools) docs(_ context.Context, _ *mcp.CallToolRequest, in DocsInput) (*mcp.CallToolResult, any, error) {
	t.log.Add("tool", "hardcover_docs", "topic", in.Topic, "name", in.Name)
	var text string
	var err error
	switch {
	case in.Topic != "" && in.Name != "":
		err = errors.New("give either topic or name, not both")
	case in.Topic != "":
		text, err = reference.Guide(in.Topic)
	case in.Name != "":
		text, err = reference.Lookup(in.Name)
	default:
		text = reference.Overview()
	}
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

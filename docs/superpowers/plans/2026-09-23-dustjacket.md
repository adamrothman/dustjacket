# Dustjacket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A remote MCP server, hosted on AWS Lambda at `https://dustjacket.rothman.tools/mcp`, that lets Adam and his sister use their own Hardcover accounts from claude.ai and the Claude apps through three tools: `hardcover_query`, `hardcover_mutate` and `hardcover_docs`.

**Architecture:** One Go binary behind CloudFront and a Lambda function URL. It has three parts:
- **An OAuth 2.1 server** adapted from pickem. Its connect page takes a person's Hardcover API key, asks Hardcover's `me` whose key it is, checks an allowlist, and stores the key sealed with KMS on that person's record in DynamoDB.
- **A stateless MCP endpoint** whose GraphQL tools pass documents through to Hardcover, with read and write kept apart, as that person.
- **A docs tool** serving guides validated against a built-in schema snapshot.

**Tech Stack:** Go (`github.com/modelcontextprotocol/go-sdk` v1.8.0, `github.com/vektah/gqlparser/v2` v2.5.58, aws-sdk-go-v2 for DynamoDB and KMS, aws-lambda-go with aws-lambda-go-api-proxy), DynamoDB, KMS, CloudFront, Terraform in `personal-infra`, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-23-dustjacket-design.md`. Executors read the spec and this plan together.

**Where the code came from:** every Go file, template, workflow and Terraform file below was built and tested before this plan was written. Together they passed:
- `go test ./...` and `go test -race ./...`
- `go vet`, `gofmt`, and the arm64 Lambda build
- `terraform fmt` and `terraform validate`

The code is therefore meant to be used as written. The only parts that depend on facts nobody has checked yet are:
- the guide text that Task 9 fills in from Task 1's live checks
- the small adjustments Task 4 makes if Task 1 finds that Hardcover's error bodies differ from its documentation

**Tasks that need Adam:**
- **Task 1:** two Hardcover keys and a test book.
- **Task 13:** AWS login, his sister's Hardcover username, and approval of the Terraform plan.
- **Task 14:** approval to apply and deploy, and connecting in claude.ai.

Stop and ask at those points. Never ask Adam to paste a key into the chat.

## Global Constraints

- **Module:** `github.com/adamrothman/dustjacket`. The Go version is whatever `go mod init` writes; CI reads it from `go.mod`.
- **CI checks:** `gofmt -l .` must print nothing, `go vet ./...` must pass, and `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/dustjacket` must succeed.
- **Direct dependencies:** only these.
  - `github.com/modelcontextprotocol/go-sdk@v1.8.0`
  - `github.com/vektah/gqlparser/v2@v2.5.58`
  - `github.com/agnivade/levenshtein@v1.2.1`
  - `github.com/aws/aws-sdk-go-v2/service/dynamodb@v1.69.0`
  - `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue@v1.21.5`
  - `github.com/aws/aws-sdk-go-v2/service/kms`
  - `github.com/aws/aws-sdk-go-v2/config@v1.33.5`
  - `github.com/aws/aws-lambda-go@v1.55.0`
  - `github.com/awslabs/aws-lambda-go-api-proxy@v0.16.2`
- **Hardcover:** endpoint `https://api.hardcover.app/v1/graphql`, header `Authorization: Bearer <key>`, User-Agent `dustjacket/<commit> (+<base URL>)`, and a 20-second timeout per request.
- **Tools:** exactly `hardcover_query`, `hardcover_mutate` and `hardcover_docs`. A result whose serialized size is over 100 KB (`100 << 10` bytes) is refused.
- **OAuth:**
  - The only scope is `hardcover`.
  - Lifetimes: access tokens 1 hour, refresh tokens 30 days (rotated on use), authorization codes 10 minutes, unused clients 24 hours.
  - Redirect hosts `claude.ai` and `claude.com`.
- **Table keys:** `USER#<hardcover user id>`, `OAUTHCLIENT#<id>`, `GRANT#<id>`, `AUTHCODE#<sha256 hex>` and `TOKEN#<sha256 hex>`, all with SK `META`. TTL attribute `ttl`.
- **KMS:** encryption context `{"hardcover_user_id": "<id>"}`.
- **Environment:** `DUSTJACKET_BASE_URL`, `DUSTJACKET_TABLE`, `DUSTJACKET_KMS_KEY_ID`, `DUSTJACKET_ALLOWED_USERS`, `DUSTJACKET_ORIGIN_VERIFY`, `DUSTJACKET_OAUTH_REDIRECT_HOSTS`, `DUSTJACKET_HARDCOVER_URL`.
- **Logs:** never query text, variables, keys, request bodies or query strings.
- **Timing:** Lambda timeout 29 s. Each request's work stops 3 s before it.
- **AWS:** account `<apps account ID>`, region `us-west-2`, profile `apps`.
- **Git:** work on the branch `implement`. Every commit message ends with the Co-Authored-By trailer from your session's attribution instructions.

## Review Focus

These are the inputs the unit tests can't fully exercise and that are most likely to bite someone using Dustjacket. Each has a test or a manual check in the task that owns it.

1. **Real browsers posting the connect form**, including the sheet the Claude phone app opens.
   - **Expected:** the browser sends `Origin: https://dustjacket.rothman.tools` and gets redirected back to Claude.
   - **Risk:** `Referrer-Policy: no-referrer` would make browsers send `Origin: null`, which the origin check rejects.
   - **Pinned by:** `TestConnectPage` asserts `same-origin` (Task 7). Task 14 checks it by hand on desktop and phone.
2. **claude.ai's actual authorize and token requests.**
   - **Expected:** a clean connect.
   - **Risk:** unexpected `scope` or `resource` values are refused with `invalid_scope` or `invalid_target`.
   - **Checked by:** reading the first connect's request log in Task 14.
3. **Hardcover's real refusal bodies** for 401, 403 and 429.
   - **Expected:** the tool messages written in `describe`.
   - **Risk:** Hardcover's documentation doesn't show exact body shapes.
   - **Pinned by:** Task 1's L9 records the real shapes, and Task 4's `TestDoRefusals` uses them.
4. **Titles, reviews and variables that aren't ASCII.**
   - **Expected:** passed through byte for byte.
   - **Pinned by:** `TestQueryPassesThrough` (Task 10).
5. **An allowlist written with spaces, empty entries or different case.**
   - **Expected:** trimmed, case-insensitive matching.
   - **Pinned by:** `TestLoadConfig` (Task 11) and `TestConnectFlow` (Task 7).

## File map

```
go.mod, go.sum, .gitignore
cmd/dustjacket/          main.go (Lambda + serve), config.go (env, build), *_test.go (config, end to end)
internal/store/          store.go (interface, Encode), memory.go, dynamo.go, store_test.go
internal/sealer/         sealer.go (Sealer, Local, KMS), sealer_test.go
internal/hardcover/      client.go (Do, Error), me.go (Me, Scopes, KeyURL), client_test.go, live_test.go (-tags live)
internal/oauth/          oauth.go (Server, metadata, register, authorize), grants.go (User, Grant, Connect, tokens, Authenticate), *_test.go
internal/reqlog/         reqlog.go, reqlog_test.go
internal/web/            server.go (routes, middleware, render), connect.go, templates/*.html, static/style.css, web_test.go
internal/reference/      reference.go (schema, Lookup), guides.go (Topics, Overview, Guide), schema.graphql, SCHEMA_LICENSE.md, guides/*.md, *_test.go
internal/mcpserver/      mcpserver.go, mcpserver_test.go
scripts/update-schema.sh
docs/                    decisions.md, runbook.md, hardcover-notes.md
.github/workflows/       ci.yml, deploy.yml
CLAUDE.md, README.md
personal-infra: terraform/aws/acct-apps/dustjacket/*.tf, CLAUDE.md; root CLAUDE.md
```

---

### Task 1: Live checks against Hardcover

These checks answer the spec's live-check questions L1–L8. They add L9, what Hardcover's refusals look like, because the client's error mapping depends on it. The answers decide the guide text (Task 9) and small parts of the client (Task 4).

**Files:**
- Create: `go.mod`, `.gitignore`
- Create: `internal/hardcover/live_test.go`
- Create: `docs/hardcover-notes.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `docs/hardcover-notes.md`, which Tasks 4, 9 and 13 read.

- [ ] **Step 1: Create the branch and the module**

The spec is committed on `design/spec`; build on it.

```bash
cd ~/src/dustjacket
git checkout design/spec
git checkout -b implement
go mod init github.com/adamrothman/dustjacket
```

Write `.gitignore`:

```text
/bin/
/dist/
*.zip
bootstrap
.env
```

- [ ] **Step 2: Write the live checks**

Write `internal/hardcover/live_test.go`. It uses only the standard library, and the build tag keeps it out of `go test ./...` and CI.

```go
//go:build live

// Live checks against the real Hardcover API (docs/hardcover-notes.md).
// They log what Hardcover does, in lines starting with the check's
// number, and fail only when a request cannot be made at all. Run by
// hand, never in CI:
//
//	HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) \
//	HARDCOVER_TEST_BOOK_ID=<a book not in the library> \
//	go test -tags live -v -count=1 ./internal/hardcover/
//
// HARDCOVER_TOKEN_NO_REVIEWS, when set, is a second key with only
// read:me:content, read:library and write:library, for L5.
// HARDCOVER_SEARCH, when set, is what the search checks look for
// ("dune" otherwise): a way to find a test book's ID.
// The mutating checks add the test book to the library and remove it
// again; the book must not already be in the library.
package hardcover

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
)

func liveEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s not set", name)
	}
	return v
}

// livePost sends a GraphQL request straight to Hardcover and returns the
// status, the headers and the decoded body.
func livePost(t *testing.T, key, query string, vars map[string]any) (int, http.Header, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequest("POST", DefaultURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dustjacket-live-checks")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return res.StatusCode, res.Header, out
}

func show(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 1500 {
		return string(b[:1500]) + "…"
	}
	return string(b)
}

// dig follows keys and indexes (ints) into decoded JSON.
func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func liveMe(t *testing.T, key string) int {
	t.Helper()
	status, h, body := livePost(t, key, `query { me { id username } }`, nil)
	t.Logf("L6: me with the main key: HTTP %d %s", status, show(body))
	t.Logf("L9: RateLimit header: %q", h.Get("RateLimit"))
	id, ok := dig(body, "data", "me", 0, "id").(float64)
	if !ok {
		t.Fatalf("me failed: %s", show(body))
	}
	return int(id)
}

func TestLiveReads(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	me := liveMe(t, key)

	_, _, body := livePost(t, key, `query { user_books(limit: 5) { id user_id } }`, nil)
	t.Logf("L3: user_books with no where (me is %d): %s", me, show(body))

	q := os.Getenv("HARDCOVER_SEARCH")
	if q == "" {
		q = "dune"
	}
	for _, qt := range []string{"book", "author", "series", "user", "list"} {
		_, _, body := livePost(t, key, `query Search($q: String!, $t: String!) { search(query: $q, query_type: $t, per_page: 2) { ids results } }`, map[string]any{"q": q, "t": qt})
		t.Logf("L4: search %s: %s", qt, show(body))
	}
}

func TestLiveErrors(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	status, h, body := livePost(t, "not-a-real-key", `query { me { id } }`, nil)
	t.Logf("L9: bad key: HTTP %d %s WWW-Authenticate=%q", status, show(body), h.Get("WWW-Authenticate"))
	status, _, body = livePost(t, key, `query { a: me { id } b: me { id } c: me { id } d: me { id } e: me { id } f: me { id } }`, nil)
	t.Logf("L9: six top-level fields: HTTP %d %s", status, show(body))
	status, _, body = livePost(t, key, `query { notification_deliveries(limit: 1) { id } }`, nil)
	t.Logf("L9: a scope the key lacks (read:notifications): HTTP %d %s", status, show(body))
}

func TestLiveWrites(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	book, err := strconv.Atoi(liveEnv(t, "HARDCOVER_TEST_BOOK_ID"))
	if err != nil {
		t.Fatal(err)
	}
	me := liveMe(t, key)

	_, _, body := livePost(t, key, `mutation Add($book: Int!) { insert_user_book(object: {book_id: $book, status_id: 1, rating: 4.5, private_notes: "dustjacket live test"}) { id error user_book { id status_id rating private_notes } } }`, map[string]any{"book": book})
	t.Logf("setup: insert_user_book: %s", show(body))
	ubf, ok := dig(body, "data", "insert_user_book", "id").(float64)
	if !ok {
		t.Fatalf("could not add the test book: %s", show(body))
	}
	ub := int(ubf)
	t.Cleanup(func() {
		_, _, body := livePost(t, key, `mutation Remove($id: Int!) { delete_user_book(id: $id) { id } }`, map[string]any{"id": ub})
		t.Logf("cleanup: delete_user_book: %s", show(body))
	})

	_, _, body = livePost(t, key, `mutation Partial($id: Int!) { update_user_book(id: $id, object: {status_id: 2}) { error user_book { status_id rating private_notes } } }`, map[string]any{"id": ub})
	t.Logf("L1: update_user_book with only status_id (rating was 4.5, private_notes set): %s", show(body))

	_, _, body = livePost(t, key, `mutation Read($ub: Int!) { insert_user_book_read(user_book_id: $ub, user_book_read: {started_at: "2026-09-01", progress_pages: 10}) { id error user_book_read { id started_at progress_pages } } }`, map[string]any{"ub": ub})
	t.Logf("setup: insert_user_book_read: %s", show(body))
	if rf, ok := dig(body, "data", "insert_user_book_read", "id").(float64); ok {
		read := int(rf)
		_, _, body = livePost(t, key, `mutation PartialRead($id: Int!) { update_user_book_read(id: $id, object: {progress_pages: 20}) { error user_book_read { started_at progress_pages } } }`, map[string]any{"id": read})
		t.Logf("L1-read: update_user_book_read with only progress_pages (started_at was 2026-09-01): %s", show(body))
		_, _, body = livePost(t, key, `mutation Seconds($id: Int!) { update_user_book_read(id: $id, object: {started_at: "2026-09-01", progress_seconds: 600}) { error user_book_read { progress_seconds progress_pages edition_id } } }`, map[string]any{"id": read})
		t.Logf("L7: progress_seconds without an audiobook edition, as returned: %s", show(body))
		_, _, body = livePost(t, key, `query ReadBack($id: Int!) { user_book_reads_by_pk(id: $id) { started_at progress_seconds progress_pages edition_id } }`, map[string]any{"id": read})
		t.Logf("L7: read back: %s", show(body))
	}

	_, _, body = livePost(t, key, `query Editions($book: Int!) { books_by_pk(id: $book) { default_physical_edition_id editions(limit: 1) { id } } }`, map[string]any{"book": book})
	edition, ok := dig(body, "data", "books_by_pk", "default_physical_edition_id").(float64)
	if !ok {
		edition, ok = dig(body, "data", "books_by_pk", "editions", 0, "id").(float64)
	}
	if ok {
		owned := func(when string) {
			_, _, body := livePost(t, key, `query Owned($me: Int!, $book: Int!, $e: Int!) { lists(where: {user_id: {_eq: $me}, slug: {_eq: "owned"}}) { list_books(where: {edition_id: {_eq: $e}}) { id } } user_books(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) { owned owned_copies } }`, map[string]any{"me": me, "book": book, "e": int(edition)})
			t.Logf("L2: owned %s: %s", when, show(body))
		}
		owned("before")
		for i := 1; i <= 2; i++ {
			_, _, body := livePost(t, key, `mutation Own($e: Int!) { edition_owned(id: $e) { id list_book { id } } }`, map[string]any{"e": int(edition)})
			t.Logf("L2: edition_owned call %d: %s", i, show(body))
			owned("after call " + strconv.Itoa(i))
		}
	} else {
		t.Logf("L2: the test book has no edition: %s", show(body))
	}

	_, _, body = livePost(t, key, `mutation Review($id: Int!) { update_user_book(id: $id, object: {status_id: 3, review_markdown: "dustjacket live test review"}) { error user_book { has_review review_markdown } } }`, map[string]any{"id": ub})
	t.Logf("L5: review with the main key: %s", show(body))
	if nr := os.Getenv("HARDCOVER_TOKEN_NO_REVIEWS"); nr != "" {
		status, _, body := livePost(t, nr, `mutation Review($id: Int!) { update_user_book(id: $id, object: {status_id: 3, review_markdown: "dustjacket live test review 2"}) { error user_book { has_review review_markdown } } }`, map[string]any{"id": ub})
		t.Logf("L5: review with a key lacking write:reviews: HTTP %d %s", status, show(body))
	} else {
		t.Logf("L5: HARDCOVER_TOKEN_NO_REVIEWS not set; skipped the second key")
	}

	for _, p := range []int{0, 3} {
		_, _, body := livePost(t, key, `mutation Journal($book: Int!, $p: Int!) { insert_reading_journal(object: {book_id: $book, event: "note", entry: "dustjacket live test", privacy_setting_id: $p, tags: []}) { id errors reading_journal { id privacy_setting_id } } }`, map[string]any{"book": book, "p": p})
		t.Logf("L8: journal note with privacy_setting_id %d: %s", p, show(body))
		if id, ok := dig(body, "data", "insert_reading_journal", "id").(float64); ok {
			_, _, body := livePost(t, key, `mutation Unjournal($id: Int!) { delete_reading_journal(id: $id) { id } }`, map[string]any{"id": int(id)})
			t.Logf("cleanup: delete_reading_journal: %s", show(body))
		}
	}
}
```

- [ ] **Step 3: Check it compiles and skips without a key**

Run: `go vet -tags live ./internal/hardcover/ && go test -tags live -count=1 -v ./internal/hardcover/`
Expected: `--- SKIP` for `TestLiveReads`, `TestLiveErrors` and `TestLiveWrites` (each says `HARDCOVER_TOKEN not set`), then `PASS`.

- [ ] **Step 4: Ask Adam for two keys and a test book**

Send Adam this (adjust the wording as you like, but keep the commands):

> For the live checks I need two Hardcover keys, each saved to a file so it never appears in our chat:
> 1. Open https://hardcover.app/account/api/keys/new?scope=read:me:content+read:catalog+read:library+write:library+read:journal+read:lists+write:lists+read:goals+write:goals+read:social+read:users+write:reviews and create a key with a short expiry. Copy it, then run `! mkdir -p ~/.config/dustjacket && pbpaste > ~/.config/dustjacket/hardcover-token && chmod 600 ~/.config/dustjacket/hardcover-token`
> 2. Open https://hardcover.app/account/api/keys/new?scope=read:me:content+read:library+write:library and create a second, short-lived key. It lacks `write:reviews`, which tells us whether that scope matters. Copy it, then run `! pbpaste > ~/.config/dustjacket/hardcover-token-no-reviews && chmod 600 ~/.config/dustjacket/hardcover-token-no-reviews`
> 3. Name a book that is **not** in your Hardcover library. The checks add it, change it, and remove it again.

Wait for Adam's answer.

- [ ] **Step 5: Find the test book's ID**

```bash
HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) HARDCOVER_SEARCH="<the title Adam gave>" \
  go test -tags live -run TestLiveReads -count=1 -v ./internal/hardcover/ 2>&1 | grep 'L4: search book'
```

Expected: a line with `"ids":[...]` and `results` naming the matches. Confirm the right book with Adam if the top match is ambiguous.

- [ ] **Step 6: Run every check, output to the scratchpad**

```bash
HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) \
HARDCOVER_TOKEN_NO_REVIEWS=$(cat ~/.config/dustjacket/hardcover-token-no-reviews) \
HARDCOVER_TEST_BOOK_ID=<id> \
  go test -tags live -count=1 -v ./internal/hardcover/ > "$SCRATCHPAD/live.txt" 2>&1; tail -5 "$SCRATCHPAD/live.txt"
```

Here `$SCRATCHPAD` is your session's scratchpad directory. The output holds Adam's user ID and username, so it never goes in the repo.

Expected: `PASS`, with log lines labeled `L1`, `L1-read`, `L2`, `L3`, `L4`, `L5`, `L6`, `L7`, `L8` and `L9`, plus `setup` and `cleanup` lines.
- If `setup: insert_user_book` shows an error, the book is probably already in the library. Pick another book and rerun.
- The `cleanup: delete_user_book` line must show an `id` and no `errors`. If it doesn't, tell Adam which book to remove by hand.

- [ ] **Step 7: Write the findings**

Create `docs/hardcover-notes.md` starting with this header:

~~~markdown
# Hardcover API notes

What the live checks (`internal/hardcover/live_test.go`, run with
`-tags live`; see the runbook) found about Hardcover's API. The guides in
`internal/reference/guides/` are written from these. When a rerun finds
something different, update this file and the guides together.

~~~

Under the header, add a line `Run on <today's date> with a key made from the connect page's scope link.`, then a table:
- Columns: `#`, `Question`, `Finding`.
- One row for each of L1, L1-read, L2, L3, L4, L5, L6, L7, L8 and L9.
- Each finding is one or two sentences stating what the log showed. Record facts about the API only: no user IDs, usernames or keys.

The questions:

| # | Question |
|---|---|
| L1 | Does `update_user_book` with only `status_id` keep the `rating` and `private_notes` it had, or null them? |
| L1-read | Does `update_user_book_read` with only `progress_pages` keep `started_at`, or null it? |
| L2 | Is `edition_owned` a toggle (owned after the first call, not owned after the second)? |
| L3 | Does `user_books` without a `where` return only the caller's rows? |
| L4 | Where are each match's fields in `search`'s `results` (e.g. `results.hits[].document`), and which fields does a book match carry? |
| L5 | Did the review save with the key lacking `write:reviews` (no error, `has_review` true)? |
| L6 | Does `me { id username }` work with a key that has `read:me:content` but not `read:me`? |
| L7 | Does `progress_seconds` persist on a read with no audiobook `edition_id`? |
| L8 | What does a journal entry with `privacy_setting_id: 0` return: an error, an empty result, or success? |
| L9 | For a bad key, six top-level fields, and a scope the key lacks: the HTTP status and the exact body shape, and the `RateLimit` header's format. |

- [ ] **Step 8: Commit**

```bash
git add go.mod .gitignore internal/hardcover/live_test.go docs/hardcover-notes.md
git commit -m "Add live checks against Hardcover's API and their findings"
```

---

### Task 2: Store

This is pickem's single-table store, trimmed to the operations Dustjacket uses. Two changes from pickem:
- A transaction op is just "put, optionally only if absent".
- An expiring item with a zero expiry gets no TTL. OAuth clients use this.

**Files:**
- Create: `internal/store/store.go`, `internal/store/memory.go`, `internal/store/dynamo.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `store.Key{PK, SK string}`
  - `store.Item` interface: `Key() (string, string)`, `ItemType() string`
  - `store.Expiring` interface: `ExpiresAt() time.Time`. A zero time means no TTL.
  - `store.Raw` with methods `Key() Key` and `Decode(out any) error`
  - `store.Op{Put Item; IfAbsent bool}`
  - `store.Store` interface: `Get(ctx, Key, out any) error`, `Put(ctx, Item) error`, `PutIfAbsent(ctx, Item) error`, `Delete(ctx, Key) error`, `Transact(ctx, ...Op) error`
  - `store.ErrNotFound`, `store.ErrConflict`
  - `store.NewMemory() *Memory`
  - `store.NewDynamo(*dynamodb.Client, table string) *Dynamo`
  - `store.Encode(Item) (Raw, error)`

- [ ] **Step 1: Add the dependencies**

```bash
go get github.com/aws/aws-sdk-go-v2/service/dynamodb@v1.69.0 github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue@v1.21.5
```

- [ ] **Step 2: Write the failing test**

```go
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type thing struct {
	ID      string     `dynamodbav:"id"`
	Note    string     `dynamodbav:"note"`
	Expires *time.Time `dynamodbav:"expires,omitempty"`
}

func (t thing) Key() (string, string) { return "THING#" + t.ID, "META" }
func (thing) ItemType() string        { return "thing" }
func (t thing) ExpiresAt() time.Time {
	if t.Expires == nil {
		return time.Time{}
	}
	return *t.Expires
}

func key(id string) Key { return Key{"THING#" + id, "META"} }

func TestRoundTripAndConflicts(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := s.Put(ctx, thing{ID: "a", Note: "first"}); err != nil {
		t.Fatal(err)
	}
	var got thing
	if err := s.Get(ctx, key("a"), &got); err != nil || got.Note != "first" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if err := s.Get(ctx, key("b"), &got); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.PutIfAbsent(ctx, thing{ID: "a"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("put-if-absent over existing: %v", err)
	}
	if err := s.Delete(ctx, key("a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestTransactIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := s.Put(ctx, thing{ID: "taken"}); err != nil {
		t.Fatal(err)
	}
	err := s.Transact(ctx, Op{Put: thing{ID: "new"}}, Op{Put: thing{ID: "taken"}, IfAbsent: true})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("transact: %v", err)
	}
	var got thing
	if err := s.Get(ctx, key("new"), &got); !errors.Is(err, ErrNotFound) {
		t.Fatal("a failed transaction wrote its other items")
	}
	if err := s.Transact(ctx, Op{Put: thing{ID: "new"}}, Op{Put: thing{ID: "other"}, IfAbsent: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, key("other"), &got); err != nil {
		t.Fatal(err)
	}
}

func TestTTLOnlyWhenSet(t *testing.T) {
	exp := time.Unix(1700000000, 0)
	r, err := Encode(thing{ID: "a", Expires: &exp})
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := r["ttl"].(*types.AttributeValueMemberN); !ok || n.Value != "1700000000" {
		t.Fatalf("ttl = %#v", r["ttl"])
	}
	if r["type"].(*types.AttributeValueMemberS).Value != "thing" || r.Key() != key("a") {
		t.Fatalf("keys and type: %#v", r)
	}
	r, err = Encode(thing{ID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r["ttl"]; ok {
		t.Fatal("ttl written for an item that does not expire")
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/store/`
Expected: FAIL to build, with errors like `undefined: NewMemory`, `undefined: Key` and `undefined: Encode`.

- [ ] **Step 4: Implement**

`internal/store/store.go`:

```go
// Package store is the single-table persistence layer: one interface with
// a DynamoDB implementation and an in-memory one that shares the same
// attribute encoding, so tests exercise the real marshalling.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: condition failed")
)

// Key identifies an item.
type Key struct{ PK, SK string }

// Item is anything storable.
type Item interface {
	Key() (pk, sk string)
	ItemType() string
}

// Expiring items get a DynamoDB TTL. A zero time means the item does not
// expire.
type Expiring interface {
	ExpiresAt() time.Time
}

// Raw is one stored item, decodable into a typed struct.
type Raw map[string]types.AttributeValue

func (r Raw) Key() Key {
	k := Key{}
	if v, ok := r["PK"].(*types.AttributeValueMemberS); ok {
		k.PK = v.Value
	}
	if v, ok := r["SK"].(*types.AttributeValueMemberS); ok {
		k.SK = v.Value
	}
	return k
}

func (r Raw) Decode(out any) error { return attributevalue.UnmarshalMap(r, out) }

// Op is one write in a transaction: put the item, and with IfAbsent,
// fail the whole transaction if an item with its key already exists.
type Op struct {
	Put      Item
	IfAbsent bool
}

type Store interface {
	Get(ctx context.Context, key Key, out any) error
	Put(ctx context.Context, item Item) error
	// PutIfAbsent fails with ErrConflict if the key exists.
	PutIfAbsent(ctx context.Context, item Item) error
	// Delete removes an item; ErrNotFound if it was not there.
	Delete(ctx context.Context, key Key) error
	// Transact applies ops atomically; an IfAbsent op whose key exists
	// yields ErrConflict and nothing is written.
	Transact(ctx context.Context, ops ...Op) error
}

// Encode marshals an item with its keys, type and TTL.
func Encode(item Item) (Raw, error) {
	m, err := attributevalue.MarshalMap(item)
	if err != nil {
		return nil, err
	}
	pk, sk := item.Key()
	if pk == "" || sk == "" {
		return nil, fmt.Errorf("store: %T has an empty key", item)
	}
	m["PK"] = &types.AttributeValueMemberS{Value: pk}
	m["SK"] = &types.AttributeValueMemberS{Value: sk}
	m["type"] = &types.AttributeValueMemberS{Value: item.ItemType()}
	if ex, ok := item.(Expiring); ok {
		if t := ex.ExpiresAt(); !t.IsZero() {
			m["ttl"] = &types.AttributeValueMemberN{Value: fmt.Sprint(t.Unix())}
		}
	}
	return Raw(m), nil
}
```

`internal/store/memory.go`:

```go
package store

import (
	"context"
	"sync"
)

// Memory is an in-memory Store with DynamoDB's semantics for the
// operations Dustjacket uses. Tests use it, and so does `dustjacket
// serve -memory`.
type Memory struct {
	mu    sync.Mutex
	items map[Key]Raw
}

func NewMemory() *Memory { return &Memory{items: map[Key]Raw{}} }

func (m *Memory) Get(_ context.Context, key Key, out any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.items[key]
	if !ok {
		return ErrNotFound
	}
	return r.Decode(out)
}

func (m *Memory) Put(_ context.Context, item Item) error {
	r, err := Encode(item)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[r.Key()] = r
	return nil
}

func (m *Memory) PutIfAbsent(ctx context.Context, item Item) error {
	return m.Transact(ctx, Op{Put: item, IfAbsent: true})
}

func (m *Memory) Delete(_ context.Context, key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[key]; !ok {
		return ErrNotFound
	}
	delete(m.items, key)
	return nil
}

func (m *Memory) Transact(_ context.Context, ops ...Op) error {
	raws := make([]Raw, len(ops))
	for i, op := range ops {
		r, err := Encode(op.Put)
		if err != nil {
			return err
		}
		raws[i] = r
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check everything first, then write: all or nothing.
	for i, op := range ops {
		if _, ok := m.items[raws[i].Key()]; ok && op.IfAbsent {
			return ErrConflict
		}
	}
	for _, r := range raws {
		m.items[r.Key()] = r
	}
	return nil
}
```

`internal/store/dynamo.go`:

```go
package store

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Dynamo is the DynamoDB Store over the single table.
type Dynamo struct {
	c     *dynamodb.Client
	table string
}

func NewDynamo(c *dynamodb.Client, table string) *Dynamo { return &Dynamo{c: c, table: table} }

func keyAV(k Key) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: k.PK},
		"SK": &types.AttributeValueMemberS{Value: k.SK},
	}
}

func (d *Dynamo) Get(ctx context.Context, key Key, out any) error {
	res, err := d.c.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &d.table, Key: keyAV(key), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	if res.Item == nil {
		return ErrNotFound
	}
	return Raw(res.Item).Decode(out)
}

func (d *Dynamo) Put(ctx context.Context, item Item) error {
	r, err := Encode(item)
	if err != nil {
		return err
	}
	_, err = d.c.PutItem(ctx, &dynamodb.PutItemInput{TableName: &d.table, Item: r})
	return err
}

func (d *Dynamo) PutIfAbsent(ctx context.Context, item Item) error {
	r, err := Encode(item)
	if err != nil {
		return err
	}
	_, err = d.c.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &d.table, Item: r, ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	var cf *types.ConditionalCheckFailedException
	if errors.As(err, &cf) {
		return ErrConflict
	}
	return err
}

func (d *Dynamo) Delete(ctx context.Context, key Key) error {
	_, err := d.c.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.table, Key: keyAV(key), ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	var cf *types.ConditionalCheckFailedException
	if errors.As(err, &cf) {
		return ErrNotFound
	}
	return err
}

func (d *Dynamo) Transact(ctx context.Context, ops ...Op) error {
	items := make([]types.TransactWriteItem, 0, len(ops))
	for _, op := range ops {
		r, err := Encode(op.Put)
		if err != nil {
			return err
		}
		put := &types.Put{TableName: &d.table, Item: r}
		if op.IfAbsent {
			put.ConditionExpression = aws.String("attribute_not_exists(PK)")
		}
		items = append(items, types.TransactWriteItem{Put: put})
	}
	_, err := d.c.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	var tx *types.TransactionCanceledException
	if errors.As(err, &tx) {
		for _, r := range tx.CancellationReasons {
			if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
				return ErrConflict
			}
		}
	}
	return err
}
```

- [ ] **Step 5: Run the tests**

Run: `go mod tidy && go test ./internal/store/ && go vet ./internal/store/ && gofmt -l internal/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/store`, and nothing from vet or gofmt.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/store
git commit -m "Add the single-table store and its in-memory twin"
```

---

### Task 3: Sealer

**Files:**
- Create: `internal/sealer/sealer.go`
- Test: `internal/sealer/sealer_test.go`

**Interfaces:**
- Produces:
  - `sealer.Sealer` interface: `Seal(ctx, userID string, plaintext []byte) ([]byte, error)`, `Open(ctx, userID string, ciphertext []byte) ([]byte, error)`
  - `sealer.NewLocal() *Local`
  - `sealer.KMS{Client KMSAPI; KeyID string}`
  - `sealer.KMSAPI`, with the `Encrypt` and `Decrypt` methods of `*kms.Client`
  - `sealer.ErrOpen`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/aws/aws-sdk-go-v2/service/kms
```

- [ ] **Step 2: Write the failing test**

```go
package sealer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

func TestLocalRoundTripBoundToUser(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	ct, err := l.Seal(ctx, "42", []byte("secret-key"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("secret-key")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	pt, err := l.Open(ctx, "42", ct)
	if err != nil || string(pt) != "secret-key" {
		t.Fatalf("open: %q %v", pt, err)
	}
	if _, err := l.Open(ctx, "43", ct); !errors.Is(err, ErrOpen) {
		t.Fatalf("opened under another user: %v", err)
	}
	if _, err := l.Open(ctx, "42", ct[:5]); !errors.Is(err, ErrOpen) {
		t.Fatalf("opened a truncated ciphertext: %v", err)
	}
	if _, err := NewLocal().Open(ctx, "42", ct); !errors.Is(err, ErrOpen) {
		t.Fatal("another Local opened this one's ciphertext")
	}
}

// fakeKMS "encrypts" into JSON carrying the key ID and context, and
// refuses to decrypt under a different one, as KMS does.
type fakeKMS struct{}

type fakeBlob struct {
	KeyID   string
	Context map[string]string
	Data    []byte
}

func (fakeKMS) Encrypt(_ context.Context, in *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	b, _ := json.Marshal(fakeBlob{KeyID: *in.KeyId, Context: in.EncryptionContext, Data: in.Plaintext})
	return &kms.EncryptOutput{CiphertextBlob: b}, nil
}

func (fakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	var b fakeBlob
	if err := json.Unmarshal(in.CiphertextBlob, &b); err != nil {
		return nil, err
	}
	if b.KeyID != *in.KeyId || !maps.Equal(b.Context, in.EncryptionContext) {
		return nil, errors.New("InvalidCiphertextException")
	}
	return &kms.DecryptOutput{Plaintext: b.Data}, nil
}

func TestKMSUsesKeyAndContext(t *testing.T) {
	ctx := context.Background()
	k := &KMS{Client: fakeKMS{}, KeyID: "arn:aws:kms:us-west-2:1:key/abc"}
	ct, err := k.Seal(ctx, "42", []byte("secret-key"))
	if err != nil {
		t.Fatal(err)
	}
	var b fakeBlob
	if err := json.Unmarshal(ct, &b); err != nil {
		t.Fatal(err)
	}
	if b.KeyID != k.KeyID || b.Context["hardcover_user_id"] != "42" {
		t.Fatalf("encrypt input: %+v", b)
	}
	pt, err := k.Open(ctx, "42", ct)
	if err != nil || string(pt) != "secret-key" {
		t.Fatalf("open: %q %v", pt, err)
	}
	if _, err := k.Open(ctx, "43", ct); err == nil {
		t.Fatal("opened under another user")
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/sealer/`
Expected: FAIL to build, with `undefined: NewLocal`, `undefined: KMS` and `undefined: ErrOpen`.

- [ ] **Step 4: Implement**

```go
// Package sealer encrypts each person's Hardcover key before it is
// stored, bound to their Hardcover user ID: a ciphertext moved onto
// another person's record does not open. On Lambda the work is done by
// KMS; tests and local runs use an in-process AES-GCM key.
package sealer

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// ErrOpen is a ciphertext that does not open for the user given.
var ErrOpen = errors.New("sealer: cannot open")

type Sealer interface {
	Seal(ctx context.Context, userID string, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, userID string, ciphertext []byte) ([]byte, error)
}

// Local seals with AES-256-GCM under a random key made when it is
// created, with the user ID as additional authenticated data. Anything
// it sealed is unreadable once the process exits.
type Local struct{ aead cipher.AEAD }

func NewLocal() *Local {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &Local{aead: aead}
}

func (l *Local) Seal(_ context.Context, userID string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, l.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return l.aead.Seal(nonce, nonce, plaintext, []byte(userID)), nil
}

func (l *Local) Open(_ context.Context, userID string, ciphertext []byte) ([]byte, error) {
	n := l.aead.NonceSize()
	if len(ciphertext) < n {
		return nil, ErrOpen
	}
	plaintext, err := l.aead.Open(nil, ciphertext[:n], ciphertext[n:], []byte(userID))
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

// KMSAPI is the part of the KMS client KMS uses.
type KMSAPI interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, opts ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, opts ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// KMS seals with a customer-managed KMS key, using the user ID as
// encryption context.
type KMS struct {
	Client KMSAPI
	KeyID  string
}

func encryptionContext(userID string) map[string]string {
	return map[string]string{"hardcover_user_id": userID}
}

func (k *KMS) Seal(ctx context.Context, userID string, plaintext []byte) ([]byte, error) {
	out, err := k.Client.Encrypt(ctx, &kms.EncryptInput{KeyId: &k.KeyID, Plaintext: plaintext, EncryptionContext: encryptionContext(userID)})
	if err != nil {
		return nil, err
	}
	return out.CiphertextBlob, nil
}

func (k *KMS) Open(ctx context.Context, userID string, ciphertext []byte) ([]byte, error) {
	out, err := k.Client.Decrypt(ctx, &kms.DecryptInput{KeyId: &k.KeyID, CiphertextBlob: ciphertext, EncryptionContext: encryptionContext(userID)})
	if err != nil {
		return nil, err
	}
	return out.Plaintext, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go mod tidy && go test ./internal/sealer/ && go vet ./internal/sealer/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/sealer`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/sealer
git commit -m "Add the sealer: KMS with the user ID as context, and a local AES-GCM twin"
```

---

### Task 4: Hardcover client

**Files:**
- Create: `internal/hardcover/client.go`, `internal/hardcover/me.go`
- Test: `internal/hardcover/client_test.go`
- Read: `docs/hardcover-notes.md` (L5, L6, L9)

**Interfaces:**
- Produces:
  - `hardcover.Client{URL, UserAgent string; HTTP *http.Client}`
  - `(*Client).Do(ctx, key string, Request) (*Response, error)`
  - `(*Client).Me(ctx, key string) (*User, error)`
  - `hardcover.Request{Query string; Variables map[string]any; OperationName string}`
  - `hardcover.Response{Body json.RawMessage; RateLimit string; HasErrors bool}`
  - `hardcover.Error{Status int; Code, Scope, RetryAfter, Body string; Err error}` with `Retryable() bool`
  - `hardcover.User{ID int; Username string}`
  - `hardcover.ErrNoUser`, `hardcover.ErrQuery`
  - `hardcover.CodeTooLarge`, `hardcover.DefaultURL`, `hardcover.Timeout`
  - `hardcover.Scopes []string`, `hardcover.KeyURL string`

- [ ] **Step 1: Write the failing test**

```go
package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// reply is one canned answer from the fake Hardcover.
type reply struct {
	status int
	header map[string]string
	body   string
}

func fake(t *testing.T, r reply) (*Client, *http.Request, *[]byte) {
	t.Helper()
	var got http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = *req
		body, _ = io.ReadAll(req.Body)
		for k, v := range r.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(r.status)
		io.WriteString(w, r.body)
	}))
	t.Cleanup(srv.Close)
	return &Client{URL: srv.URL, UserAgent: "dustjacket/test"}, &got, &body
}

func TestDoSendsTheRequest(t *testing.T) {
	c, got, body := fake(t, reply{status: 200, header: map[string]string{"RateLimit": `"Free";r=59;t=60`}, body: `{"data":{"me":[{"id":1}]}}`})
	res, err := c.Do(context.Background(), "k-123", Request{Query: "query Q($n: Int!) { me { id } }", Variables: map[string]any{"n": 1}, OperationName: "Q"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.Header.Get("Authorization") != "Bearer k-123" || got.Header.Get("User-Agent") != "dustjacket/test" || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request: %s %v", got.Method, got.Header)
	}
	var sent map[string]any
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["query"] != "query Q($n: Int!) { me { id } }" || sent["operationName"] != "Q" || sent["variables"].(map[string]any)["n"] != float64(1) {
		t.Fatalf("body: %s", *body)
	}
	if string(res.Body) != `{"data":{"me":[{"id":1}]}}` || res.RateLimit != `"Free";r=59;t=60` || res.HasErrors {
		t.Fatalf("response: %+v", res)
	}
}

func TestDoGraphQLErrors(t *testing.T) {
	c, _, _ := fake(t, reply{status: 200, body: `{"errors":[{"message":"field 'nope' not found"}]}`})
	res, err := c.Do(context.Background(), "k", Request{Query: "{ nope }"})
	if err != nil || !res.HasErrors {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDoRefusals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reply     reply
		want      Error
		retryable bool
	}{
		{"invalid token", reply{status: 401, body: `{"error":"invalid_token"}`}, Error{Status: 401, Code: "invalid_token"}, false},
		{"missing scope", reply{status: 403, body: `{"error":"insufficient_scope","scope":"read:lists"}`}, Error{Status: 403, Code: "insufficient_scope", Scope: "read:lists"}, false},
		{"too many fields", reply{status: 403, body: `{"error":"top_level_limit_exceeded"}`}, Error{Status: 403, Code: "top_level_limit_exceeded"}, false},
		{"rate limited", reply{status: 429, header: map[string]string{"Retry-After": "12"}, body: `{"error":"rate_limited"}`}, Error{Status: 429, Code: "rate_limited", RetryAfter: "12"}, false},
		{"unavailable", reply{status: 503, body: `upstream down`}, Error{Status: 503}, true},
		{"timeout", reply{status: 408, body: ``}, Error{Status: 408}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := fake(t, tc.reply)
			_, err := c.Do(context.Background(), "k", Request{Query: "{ me { id } }"})
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error %T %v", err, err)
			}
			if e.Status != tc.want.Status || e.Code != tc.want.Code || e.Scope != tc.want.Scope || e.RetryAfter != tc.want.RetryAfter {
				t.Fatalf("got %+v, want %+v", e, tc.want)
			}
			if e.Retryable() != tc.retryable {
				t.Fatalf("retryable = %v", e.Retryable())
			}
		})
	}
}

func TestDoNotJSON(t *testing.T) {
	c, _, _ := fake(t, reply{status: 200, body: `<html>oops</html>`})
	_, err := c.Do(context.Background(), "k", Request{Query: "{ me { id } }"})
	var e *Error
	if !errors.As(err, &e) || e.Err == nil || e.Retryable() {
		t.Fatalf("%v", err)
	}
}

func TestDoTooLarge(t *testing.T) {
	c, _, _ := fake(t, reply{status: 200, body: `{"data":"` + strings.Repeat("x", maxBody) + `"}`})
	_, err := c.Do(context.Background(), "k", Request{Query: "{ me { id } }"})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTooLarge {
		t.Fatalf("%v", err)
	}
}

func TestDoTimeoutIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slower than the caller's deadline, but not forever: Close waits
		// for handlers to return.
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (&Client{URL: srv.URL}).Do(ctx, "k", Request{Query: "{ me { id } }"})
	var e *Error
	if !errors.As(err, &e) || e.Status != 0 || !e.Retryable() {
		t.Fatalf("%v", err)
	}
}

func TestMe(t *testing.T) {
	c, _, body := fake(t, reply{status: 200, body: `{"data":{"me":[{"id":7,"username":"adam"}]}}`})
	u, err := c.Me(context.Background(), "k")
	if err != nil || u.ID != 7 || u.Username != "adam" {
		t.Fatalf("%+v %v", u, err)
	}
	if !strings.Contains(string(*body), "me { id username }") {
		t.Fatalf("query: %s", *body)
	}

	c, _, _ = fake(t, reply{status: 200, body: `{"data":{"me":[]}}`})
	if _, err := c.Me(context.Background(), "k"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("empty me: %v", err)
	}
	c, _, _ = fake(t, reply{status: 200, body: `{"errors":[{"message":"field 'me' not found in type: 'query_root'"}]}`})
	if _, err := c.Me(context.Background(), "k"); !errors.Is(err, ErrQuery) {
		t.Fatalf("graphql error: %v", err)
	}
	c, _, _ = fake(t, reply{status: 401, body: `{"error":"invalid_token"}`})
	var e *Error
	if _, err := c.Me(context.Background(), "k"); !errors.As(err, &e) || e.Status != 401 {
		t.Fatalf("401: %v", err)
	}
}

func TestKeyURL(t *testing.T) {
	want := "https://hardcover.app/account/api/keys/new?scope=read:me:content+read:catalog+read:library+write:library+read:journal+read:lists+write:lists+read:goals+write:goals+read:social+read:users+write:reviews"
	if KeyURL != want {
		t.Fatalf("KeyURL = %s", KeyURL)
	}
}
```

- [ ] **Step 2: Bring the test in line with the live checks**

Read `docs/hardcover-notes.md` and adjust the test before implementing:
- **L5.** If the review saved with the key lacking `write:reviews`, remove `+write:reviews` from `want` in `TestKeyURL`. Step 4 removes it from `Scopes` to match.
- **L6.** If `me` failed with a key that had `read:me:content` but not `read:me`, stop and tell Adam. The connect flow depends on that scope, and the design needs revisiting.
- **L9.** For each refusal whose recorded body differs from the documented `{"error": "...", "scope": "..."}` shape, replace the `body` in the matching `TestDoRefusals` case with the recorded body. Keep the expected `Status`, `Code` and `Scope`.
  - Example: if the missing-scope body was recorded as `{"message":"...","code":"insufficient_scope","required":"read:lists"}`, write exactly that. The expectation stays `Code: "insufficient_scope", Scope: "read:lists"`.
  - If a bad key came back as HTTP 200 with GraphQL `errors` rather than a 401, add a case with that body, expecting `Status: 401, Code: "invalid_token"`.

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/hardcover/`
Expected: FAIL to build, with `undefined: Client`, `undefined: Request`, `undefined: Error` and `undefined: maxBody`.

- [ ] **Step 4: Implement**

`internal/hardcover/client.go`:

```go
// Package hardcover is a small client for Hardcover's GraphQL API: it
// sends a document with one person's key and hands back the response
// body verbatim, or an *Error saying why Hardcover refused it.
package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultURL = "https://api.hardcover.app/v1/graphql"

// Timeout bounds one request. It sits inside the Lambda invocation's
// budget, leaving time to answer when Hardcover is slow.
const Timeout = 20 * time.Second

// maxBody caps how much of a response is read: far beyond what the MCP
// tools pass on, so a bigger one is refused rather than buffered.
const maxBody = 4 << 20

// CodeTooLarge is the Error.Code of a response over maxBody.
const CodeTooLarge = "response_too_large"

type Client struct {
	URL       string       // DefaultURL when empty
	UserAgent string       // sent when set
	HTTP      *http.Client // a client with Timeout when nil
}

type Request struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// Response is a request GraphQL ran, successfully or not.
type Response struct {
	Body      json.RawMessage // the JSON body, verbatim
	RateLimit string          // the RateLimit header, verbatim
	HasErrors bool            // the body has a non-empty "errors"
}

// Error is Hardcover refusing a request, or the request failing to get
// an answer at all (Status 0).
type Error struct {
	Status     int    // HTTP status; 0 for timeouts and network failures
	Code       string // the body's "error", e.g. "invalid_token"
	Scope      string // the body's "scope", with insufficient_scope
	RetryAfter string // the Retry-After header, with 429
	Body       string // the body, trimmed
	Err        error  // the cause, for failures without a usable answer
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil:
		return "hardcover: " + e.Err.Error()
	case e.Code != "":
		return fmt.Sprintf("hardcover: HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("hardcover: HTTP %d", e.Status)
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable is a timeout, a network failure, a 408 or a 5xx: sending
// the same request again may work.
func (e *Error) Retryable() bool {
	return e.Status == 0 || e.Status == http.StatusRequestTimeout || e.Status >= 500
}

func (c *Client) url() string {
	if c.URL == "" {
		return DefaultURL
	}
	return c.URL
}

func (c *Client) client() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: Timeout}
	}
	return c.HTTP
}

// Do sends req with key as the bearer token.
func (c *Client) Do(ctx context.Context, key string, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+key)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		hreq.Header.Set("User-Agent", c.UserAgent)
	}
	res, err := c.client().Do(hreq)
	if err != nil {
		return nil, &Error{Err: err}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, &Error{Status: res.StatusCode, Err: err}
	}
	if len(raw) > maxBody {
		return nil, &Error{Status: res.StatusCode, Code: CodeTooLarge}
	}
	if res.StatusCode != http.StatusOK {
		return nil, refusal(res, raw)
	}
	var probe struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &Error{Status: res.StatusCode, Body: trim(raw), Err: fmt.Errorf("response is not JSON: %w", err)}
	}
	return &Response{Body: raw, RateLimit: res.Header.Get("RateLimit"), HasErrors: len(probe.Errors) > 0}, nil
}

func refusal(res *http.Response, raw []byte) *Error {
	e := &Error{Status: res.StatusCode, RetryAfter: res.Header.Get("Retry-After"), Body: trim(raw)}
	var b struct {
		Error string `json:"error"`
		Scope string `json:"scope"`
	}
	if json.Unmarshal(raw, &b) == nil {
		e.Code, e.Scope = b.Error, b.Scope
	}
	return e
}

func trim(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}
```

`internal/hardcover/me.go`. If L5 said the scope doesn't matter, drop `"write:reviews",` from `Scopes`.

```go
package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// User is the person a key belongs to.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// ErrNoUser is a key that authenticated but whose `me` is empty.
var ErrNoUser = errors.New("hardcover: me returned no user")

// ErrQuery is GraphQL rejecting the `me` query, which is valid, so the
// key lacks permission to run it.
var ErrQuery = errors.New("hardcover: me failed")

// Me asks Hardcover whose key this is. `me` returns a list whose one
// element is the key's owner.
func (c *Client) Me(ctx context.Context, key string) (*User, error) {
	res, err := c.Do(ctx, key, Request{Query: "query Me { me { id username } }"})
	if err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			Me []User `json:"me"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, err
	}
	if len(body.Errors) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrQuery, body.Errors[0].Message)
	}
	if len(body.Data.Me) == 0 || body.Data.Me[0].Username == "" {
		return nil, ErrNoUser
	}
	return &body.Data.Me[0], nil
}

// Scopes is what a Dustjacket key needs: the smallest set covering the
// use cases in docs/decisions.md. KeyURL pre-selects them.
var Scopes = []string{
	"read:me:content",
	"read:catalog",
	"read:library", "write:library",
	"read:journal",
	"read:lists", "write:lists",
	"read:goals", "write:goals",
	"read:social",
	"read:users",
	"write:reviews",
}

// KeyURL opens Hardcover's new-key page with Scopes selected.
var KeyURL = "https://hardcover.app/account/api/keys/new?scope=" + strings.Join(Scopes, "+")
```

If Step 2 changed any refusal bodies, change `refusal` in `client.go` so that it reads the recorded shape into the same `Code` and `Scope`. If the change was a 200-with-errors bad-key case, change `Do` to recognize that response before the `HasErrors` probe.

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/hardcover/ && go vet ./internal/hardcover/ && go vet -tags live ./internal/hardcover/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/hardcover`. `TestDoTimeoutIsRetryable` takes about 0.3 s.

- [ ] **Step 6: Commit**

```bash
git add internal/hardcover
git commit -m "Add the Hardcover GraphQL client, Me, and the key scopes"
```

---

### Task 5: OAuth server: metadata, registration, authorize requests

This is adapted from pickem's `internal/oauth`. The changes:
- There is one scope, `hardcover`.
- `client()` treats a client past its `Expires` as unknown.
- `Register` gives new clients a 24-hour expiry.
- `ParseAuthorize` no longer carries scopes.
- The ID, token and hash helpers are local.

**Files:**
- Create: `internal/oauth/oauth.go`
- Test: `internal/oauth/oauth_test.go`

**Interfaces:**
- Consumes: `store.Store`, `store.ErrNotFound` (Task 2); `sealer.Sealer` (Task 3).
- Produces:
  - `oauth.Server{Store store.Store; Sealer sealer.Sealer; Issuer string; AllowedRedirectHosts []string; Now func() time.Time}`
  - Handlers `Metadata`, `ProtectedResourceMetadata` and `Register`, each an `http.HandlerFunc`
  - `(*Server).ParseAuthorize(*http.Request) (*AuthorizeRequest, error)`
  - `oauth.AuthorizeRequest{Client Client; RedirectURI, State, Challenge string}`, with `Redirect(w, r, params map[string]string)`
  - `oauth.RedirectError{Code, Desc string}`
  - `oauth.Client`
  - `oauth.Scope`
  - The TTL constants
  - Unexported helpers for Task 6: `(*Server).client(ctx, id)`, `(*Server).now()`, `verifyPKCE`, `newID`, `newToken`, `hash`, `writeJSON`, `oauthError`

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
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

func newServer() (*Server, *store.Memory, *clock) {
	st := store.NewMemory()
	clk := &clock{t: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	return &Server{Store: st, Sealer: sealer.NewLocal(), Issuer: issuer, AllowedRedirectHosts: []string{"claude.ai", "claude.com"}, Now: clk.now}, st, clk
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
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/oauth/`
Expected: FAIL to build, with `undefined: Server`, `undefined: Client` and `undefined: RedirectError`.

- [ ] **Step 3: Implement**

```go
// Package oauth makes Dustjacket an OAuth 2.1 authorization server for MCP
// clients: authorization code with PKCE (S256), opaque tokens stored
// hashed, 1-hour access tokens with rotating 30-day refresh tokens, and
// dynamic client registration restricted to allowed redirect hosts. There
// is no login: on the connect page a person pastes their Hardcover key,
// and a grant is what lets a client use that key, sealed, from then on.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

const (
	// Scope is the one scope there is: using the person's Hardcover key.
	Scope = "hardcover"

	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	AuthCodeTTL     = 10 * time.Minute
	// UnusedClientTTL is how long a registered client lives if nobody
	// connects through it (docs/decisions.md 7).
	UnusedClientTTL = 24 * time.Hour
)

// Client is a registered OAuth client. Expires is set until someone
// connects through it, and cleared then.
type Client struct {
	ID           string     `dynamodbav:"id"`
	SecretHash   string     `dynamodbav:"secret_hash,omitempty"`
	Name         string     `dynamodbav:"name"`
	RedirectURIs []string   `dynamodbav:"redirect_uris"`
	CreatedAt    time.Time  `dynamodbav:"created_at"`
	Expires      *time.Time `dynamodbav:"expires,omitempty"`
}

func (c Client) Key() (string, string) { return "OAUTHCLIENT#" + c.ID, "META" }
func (Client) ItemType() string        { return "oauth_client" }
func (c Client) ExpiresAt() time.Time {
	if c.Expires == nil {
		return time.Time{}
	}
	return *c.Expires
}

// Server is the authorization server.
type Server struct {
	Store  store.Store
	Sealer sealer.Sealer
	Issuer string // "https://dustjacket.rothman.tools"
	// Hosts a registered redirect URI may point at. Empty allows any
	// https host, which is not what you want in production.
	AllowedRedirectHosts []string
	Now                  func() time.Time // time.Now when nil
}

func (s *Server) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Metadata is RFC 8414.
func (s *Server) Metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     s.Issuer,
		"authorization_endpoint":                     s.Issuer + "/oauth/authorize",
		"token_endpoint":                             s.Issuer + "/oauth/token",
		"revocation_endpoint":                        s.Issuer + "/oauth/revoke",
		"registration_endpoint":                      s.Issuer + "/oauth/register",
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_post"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"scopes_supported":                           []string{Scope},
	})
}

// ProtectedResourceMetadata is RFC 9728, which MCP clients use to find
// the authorization server for /mcp.
func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.Issuer + "/mcp",
		"authorization_servers":    []string{s.Issuer},
		"scopes_supported":         []string{Scope},
		"bearer_methods_supported": []string{"header"},
	})
}

// Register is RFC 7591 dynamic client registration, open but confined to
// the allowed redirect hosts. A client gives nothing until someone on the
// allowlist connects through it, and expires unused after a day.
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "bad JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !s.redirectAllowed(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URI host not allowed: "+u)
			return
		}
	}
	now := s.now()
	expires := now.Add(UnusedClientTTL)
	c := Client{ID: newID(), Name: strings.TrimSpace(req.ClientName), RedirectURIs: req.RedirectURIs, CreatedAt: now, Expires: &expires}
	if c.Name == "" {
		c.Name = "unnamed client"
	}
	resp := map[string]any{
		"client_id":                  c.ID,
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"client_id_issued_at":        c.CreatedAt.Unix(),
	}
	if req.TokenEndpointAuthMethod == "client_secret_post" || req.TokenEndpointAuthMethod == "client_secret_basic" {
		secret := newToken()
		c.SecretHash = hash(secret)
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
		resp["token_endpoint_auth_method"] = "client_secret_post"
	}
	if err := s.Store.PutIfAbsent(r.Context(), c); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Fragment != "" {
		return false
	}
	if len(s.AllowedRedirectHosts) == 0 {
		return true
	}
	for _, h := range s.AllowedRedirectHosts {
		if strings.EqualFold(u.Hostname(), h) {
			return true
		}
	}
	return false
}

// client loads a registered client; one past its expiry is as good as
// gone, since DynamoDB deletes expired items only eventually.
func (s *Server) client(ctx context.Context, id string) (*Client, error) {
	var c Client
	if err := s.Store.Get(ctx, store.Key{PK: "OAUTHCLIENT#" + id, SK: "META"}, &c); err != nil {
		return nil, err
	}
	if c.Expires != nil && !s.now().Before(*c.Expires) {
		return nil, store.ErrNotFound
	}
	return &c, nil
}

// AuthorizeRequest is a parsed, validated /oauth/authorize query.
type AuthorizeRequest struct {
	Client      Client
	RedirectURI string
	State       string
	Challenge   string
}

// ParseAuthorize validates the query. Errors that cannot be sent to the
// redirect URI (bad client, bad redirect) come back with a nil request,
// to show to the person; the rest come back as *RedirectError with the
// request, to redirect per RFC 6749 §4.1.2.1.
func (s *Server) ParseAuthorize(r *http.Request) (*AuthorizeRequest, error) {
	q := r.URL.Query()
	c, err := s.client(r.Context(), q.Get("client_id"))
	if err != nil {
		return nil, errors.New("unknown client_id")
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if !slices.Contains(c.RedirectURIs, redirect) {
		return nil, errors.New("redirect_uri is not registered for this client")
	}
	req := &AuthorizeRequest{Client: *c, RedirectURI: redirect, State: q.Get("state")}
	if q.Get("response_type") != "code" {
		return req, &RedirectError{Code: "unsupported_response_type", Desc: "only code is supported"}
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 {
		return req, &RedirectError{Code: "invalid_request", Desc: "PKCE S256 code_challenge required"}
	}
	req.Challenge = q.Get("code_challenge")
	for _, sc := range strings.Fields(q.Get("scope")) {
		if sc != Scope {
			return req, &RedirectError{Code: "invalid_scope", Desc: "unknown scope " + sc}
		}
	}
	if res := q.Get("resource"); res != "" && res != s.Issuer+"/mcp" {
		return req, &RedirectError{Code: "invalid_target", Desc: "unknown resource"}
	}
	return req, nil
}

// RedirectError goes back to the client via the redirect URI.
type RedirectError struct{ Code, Desc string }

func (e *RedirectError) Error() string { return e.Code + ": " + e.Desc }

// Redirect sends the client an error or a code.
func (req *AuthorizeRequest) Redirect(w http.ResponseWriter, r *http.Request, params map[string]string) {
	u, _ := url.Parse(req.RedirectURI)
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// newID is a 128-bit random identifier.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// newToken is a 256-bit random token, URL-safe.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hash is how tokens and codes are stored: never the token itself.
func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/oauth/ && go vet ./internal/oauth/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/oauth`.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth
git commit -m "Add the OAuth server's metadata, registration and authorize parsing"
```

---

### Task 6: OAuth server: connect, tokens, authenticate

**Files:**
- Create: `internal/oauth/grants.go`
- Test: `internal/oauth/grants_test.go`

**Interfaces:**
- Consumes: everything from Task 5.
- Produces:
  - `oauth.User{ID, Username string; KeyCiphertext []byte; KeyUpdatedAt, CreatedAt time.Time}`
  - `oauth.Grant{ID, ClientID, ClientName, UserID string; CreatedAt time.Time}`
  - `(*Server).Connect(ctx, *AuthorizeRequest, userID, username, key string) (code string, err error)`
  - Handlers `Token` and `Revoke`
  - `(*Server).Authenticate(ctx, *http.Request) (*Caller, error)`
  - `oauth.Caller{UserID, Username, GrantID, Key string}`
  - `oauth.ErrUnauthorized`
  - `(*Server).Challenge(http.ResponseWriter)`
  - `(*Server).MintAccessToken(ctx, grantID string) (string, error)`, for tests

- [ ] **Step 1: Write the failing test**

```go
package oauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/adamrothman/dustjacket/internal/store"
)

// connect registers a client, parses its authorize request and connects
// the given user through it, returning the client ID and the code.
func connect(t *testing.T, s *Server, userID, username, key string) (string, string) {
	t.Helper()
	_, out := register(t, s, callback)
	id := out["client_id"].(string)
	req, err := parse(s, authorizeQuery(id))
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.Connect(t.Context(), req, userID, username, key)
	if err != nil {
		t.Fatal(err)
	}
	return id, code
}

func postToken(s *Server, form url.Values) (int, map[string]any) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Token(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func exchange(s *Server, clientID, code, v string) (int, map[string]any) {
	return postToken(s, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {v}, "redirect_uri": {callback}})
}

func authenticate(s *Server, access string) (*Caller, error) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	return s.Authenticate(r.Context(), r)
}

func TestConnectStoresSealedKeyAndMakesClientPermanent(t *testing.T) {
	s, st, _ := newServer()
	clientID, _ := connect(t, s, "7", "adam", "hc-key-1")
	var u User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &u); err != nil {
		t.Fatal(err)
	}
	if u.Username != "adam" || len(u.KeyCiphertext) == 0 || strings.Contains(string(u.KeyCiphertext), "hc-key-1") {
		t.Fatalf("user: %+v", u)
	}
	var c Client
	if err := st.Get(t.Context(), store.Key{PK: "OAUTHCLIENT#" + clientID, SK: "META"}, &c); err != nil || c.Expires != nil {
		t.Fatalf("client after connect: %+v %v", c, err)
	}
}

func TestCodeExchangeRefreshRevoke(t *testing.T) {
	s, _, clk := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")

	if status, out := exchange(s, clientID, code, "wrong-verifier-wrong-verifier-wrong-verifier-x"); status != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %d %v", status, out)
	}
	// The failed attempt used the code up.
	if status, _ := exchange(s, clientID, code, verifier); status != 400 {
		t.Fatalf("code reused: %d", status)
	}

	clientID2, code := connect(t, s, "7", "adam", "hc-key-1")
	status, tok := exchange(s, clientID2, code, verifier)
	if status != 200 || tok["token_type"] != "Bearer" || tok["scope"] != "hardcover" {
		t.Fatalf("exchange: %d %v", status, tok)
	}
	caller, err := authenticate(s, tok["access_token"].(string))
	if err != nil || caller.UserID != "7" || caller.Username != "adam" || caller.Key != "hc-key-1" {
		t.Fatalf("authenticate: %+v %v", caller, err)
	}

	// Refresh rotates: the new pair works, the old refresh token doesn't.
	status, tok2 := postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID2}, "refresh_token": {tok["refresh_token"].(string)}})
	if status != 200 {
		t.Fatalf("refresh: %d %v", status, tok2)
	}
	if status, _ := postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID2}, "refresh_token": {tok["refresh_token"].(string)}}); status != 400 {
		t.Fatalf("old refresh token reused: %d", status)
	}

	// A connected client does not expire.
	clk.t = clk.t.Add(48 * time.Hour)
	status, tok3 := postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID2}, "refresh_token": {tok2["refresh_token"].(string)}})
	if status != 200 {
		t.Fatalf("refresh two days later: %d %v", status, tok3)
	}
	// Access tokens do.
	if _, err := authenticate(s, tok2["access_token"].(string)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired access token: %v", err)
	}

	// Revoking any token of the grant revokes the grant.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/revoke", strings.NewReader(url.Values{"token": {tok3["refresh_token"].(string)}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Revoke(w, r)
	if w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if _, err := authenticate(s, tok3["access_token"].(string)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestReconnectReplacesTheKeyForEveryGrant(t *testing.T) {
	s, st, clk := newServer()
	clientID, code := connect(t, s, "7", "adam", "old-key")
	_, tok := exchange(s, clientID, code, verifier)
	var before User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &before); err != nil {
		t.Fatal(err)
	}

	clk.t = clk.t.Add(time.Minute)
	connect(t, s, "7", "adam", "new-key") // through another client

	caller, err := authenticate(s, tok["access_token"].(string))
	if err != nil || caller.Key != "new-key" {
		t.Fatalf("first grant after reconnect: %+v %v", caller, err)
	}
	var after User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &after); err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) || !after.KeyUpdatedAt.Equal(clk.t) {
		t.Fatalf("created %v→%v, key updated %v", before.CreatedAt, after.CreatedAt, after.KeyUpdatedAt)
	}
}

func TestAuthenticateFailures(t *testing.T) {
	s, st, _ := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")
	_, tok := exchange(s, clientID, code, verifier)
	access := tok["access_token"].(string)

	for name, header := range map[string]string{"none": "", "basic": "Basic abc", "unknown": "Bearer nope", "refresh as access": "Bearer " + tok["refresh_token"].(string)} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if _, err := s.Authenticate(r.Context(), r); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Deleting the user record cuts off every connection they have.
	if err := st.Delete(t.Context(), store.Key{PK: "USER#7", SK: "META"}); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, access); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted user: %v", err)
	}
}

func TestChallenge(t *testing.T) {
	s, _, _ := newServer()
	w := httptest.NewRecorder()
	s.Challenge(w)
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != `Bearer resource_metadata="https://dustjacket.test/.well-known/oauth-protected-resource"` {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/oauth/`
Expected: FAIL to build, with `s.Connect undefined`, `s.Token undefined`, `undefined: User` and `undefined: ErrUnauthorized`.

- [ ] **Step 3: Implement**

```go
package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/store"
)

// User is one person, keyed by their Hardcover user ID, with their
// Hardcover key sealed to that ID. All of their grants use it, so
// reconnecting with a new key updates every connection they have.
type User struct {
	ID            string    `dynamodbav:"user_id"`
	Username      string    `dynamodbav:"username"`
	KeyCiphertext []byte    `dynamodbav:"key_ciphertext"`
	KeyUpdatedAt  time.Time `dynamodbav:"key_updated_at"`
	CreatedAt     time.Time `dynamodbav:"created_at"`
}

func (u User) Key() (string, string) { return "USER#" + u.ID, "META" }
func (User) ItemType() string        { return "user" }

// Grant is one connection: a client acting for a user. Tokens point at
// it; deleting it revokes them all.
type Grant struct {
	ID         string    `dynamodbav:"id"`
	ClientID   string    `dynamodbav:"client_id"`
	ClientName string    `dynamodbav:"client_name"`
	UserID     string    `dynamodbav:"user_id"`
	CreatedAt  time.Time `dynamodbav:"created_at"`
}

func (g Grant) Key() (string, string) { return "GRANT#" + g.ID, "META" }
func (Grant) ItemType() string        { return "grant" }

type authCode struct {
	Hash        string    `dynamodbav:"hash"`
	ClientID    string    `dynamodbav:"client_id"`
	RedirectURI string    `dynamodbav:"redirect_uri"`
	Challenge   string    `dynamodbav:"challenge"`
	GrantID     string    `dynamodbav:"grant_id"`
	Expires     time.Time `dynamodbav:"expires"`
}

func (c authCode) Key() (string, string) { return "AUTHCODE#" + c.Hash, "META" }
func (authCode) ItemType() string        { return "auth_code" }
func (c authCode) ExpiresAt() time.Time  { return c.Expires }

const (
	kindAccess  = "access"
	kindRefresh = "refresh"
)

type token struct {
	Hash     string    `dynamodbav:"hash"`
	Kind     string    `dynamodbav:"kind"`
	GrantID  string    `dynamodbav:"grant_id"`
	ClientID string    `dynamodbav:"client_id"`
	Issued   time.Time `dynamodbav:"issued"`
	Expires  time.Time `dynamodbav:"expires"`
}

func (t token) Key() (string, string) { return "TOKEN#" + t.Hash, "META" }
func (token) ItemType() string        { return "token" }
func (t token) ExpiresAt() time.Time  { return t.Expires }

// Connect records a successful connect, all in one transaction: the user
// with their key sealed (replacing any earlier key), a grant for this
// client, the client made permanent, and a single-use authorization code,
// which it returns. The caller has already checked the key with Hardcover
// and the username against the allowlist.
func (s *Server) Connect(ctx context.Context, req *AuthorizeRequest, userID, username, key string) (string, error) {
	now := s.now()
	sealed, err := s.Sealer.Seal(ctx, userID, []byte(key))
	if err != nil {
		return "", fmt.Errorf("seal key: %w", err)
	}
	u := User{ID: userID, Username: username, KeyCiphertext: sealed, KeyUpdatedAt: now, CreatedAt: now}
	var prev User
	switch err := s.Store.Get(ctx, store.Key{PK: "USER#" + userID, SK: "META"}, &prev); {
	case err == nil:
		u.CreatedAt = prev.CreatedAt
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	client := req.Client
	client.Expires = nil
	g := Grant{ID: newID(), ClientID: client.ID, ClientName: client.Name, UserID: userID, CreatedAt: now}
	code := newToken()
	c := authCode{Hash: hash(code), ClientID: client.ID, RedirectURI: req.RedirectURI, Challenge: req.Challenge, GrantID: g.ID, Expires: now.Add(AuthCodeTTL)}
	err = s.Store.Transact(ctx, store.Op{Put: u}, store.Op{Put: client}, store.Op{Put: g}, store.Op{Put: c, IfAbsent: true})
	if err != nil {
		return "", err
	}
	return code, nil
}

// Token is the token endpoint.
func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	client, err := s.client(ctx, r.PostFormValue("client_id"))
	if err != nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	if client.SecretHash != "" && subtle.ConstantTimeCompare([]byte(hash(r.PostFormValue("client_secret"))), []byte(client.SecretHash)) != 1 {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "bad client secret")
		return
	}
	now := s.now()
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		var c authCode
		key := store.Key{PK: "AUTHCODE#" + hash(r.PostFormValue("code")), SK: "META"}
		if err := s.Store.Get(ctx, key, &c); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown code")
			return
		}
		// Single use: whoever deletes it first wins.
		if err := s.Store.Delete(ctx, key); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code already used")
			return
		}
		if c.ClientID != client.ID || now.After(c.Expires) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code expired or wrong client")
			return
		}
		if uri := r.PostFormValue("redirect_uri"); uri != "" && uri != c.RedirectURI {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
			return
		}
		if !verifyPKCE(r.PostFormValue("code_verifier"), c.Challenge) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}
		s.issue(w, ctx, c.GrantID, client.ID, now)
	case "refresh_token":
		var t token
		key := store.Key{PK: "TOKEN#" + hash(r.PostFormValue("refresh_token")), SK: "META"}
		if err := s.Store.Get(ctx, key, &t); err != nil || t.Kind != kindRefresh || t.ClientID != client.ID || now.After(t.Expires) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "bad refresh token")
			return
		}
		if err := s.Store.Delete(ctx, key); err != nil { // rotation: the old one dies
			oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already used")
			return
		}
		s.issue(w, ctx, t.GrantID, client.ID, now)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (s *Server) issue(w http.ResponseWriter, ctx context.Context, grantID, clientID string, now time.Time) {
	if _, err := s.grant(ctx, grantID); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "grant revoked")
		return
	}
	access, refresh := newToken(), newToken()
	err := s.Store.Transact(ctx,
		store.Op{Put: token{Hash: hash(access), Kind: kindAccess, GrantID: grantID, ClientID: clientID, Issued: now, Expires: now.Add(AccessTokenTTL)}},
		store.Op{Put: token{Hash: hash(refresh), Kind: kindRefresh, GrantID: grantID, ClientID: clientID, Issued: now, Expires: now.Add(RefreshTokenTTL)}},
	)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": int(AccessTokenTTL.Seconds()),
		"refresh_token": refresh, "scope": Scope,
	})
}

// Revoke is RFC 7009: revoking either token kind revokes the whole grant.
func (s *Server) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	var t token
	if err := s.Store.Get(r.Context(), store.Key{PK: "TOKEN#" + hash(r.PostFormValue("token")), SK: "META"}, &t); err == nil {
		_ = s.Store.Delete(r.Context(), store.Key{PK: "GRANT#" + t.GrantID, SK: "META"})
	}
	w.WriteHeader(http.StatusOK) // always, per the RFC
}

func (s *Server) grant(ctx context.Context, id string) (*Grant, error) {
	var g Grant
	if err := s.Store.Get(ctx, store.Key{PK: "GRANT#" + id, SK: "META"}, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// Caller is who an MCP request acts for, with their Hardcover key opened.
type Caller struct {
	UserID   string
	Username string
	GrantID  string
	Key      string
}

// ErrUnauthorized is a missing, unknown, expired or revoked token.
var ErrUnauthorized = errors.New("oauth: unauthorized")

// Authenticate resolves a bearer token to its caller: token, grant, user,
// then the user's key opened. A token that leads nowhere is
// ErrUnauthorized; any other error is the server's own failure.
func (s *Server) Authenticate(ctx context.Context, r *http.Request) (*Caller, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return nil, ErrUnauthorized
	}
	var t token
	if err := s.Store.Get(ctx, store.Key{PK: "TOKEN#" + hash(strings.TrimSpace(h[7:])), SK: "META"}, &t); err != nil {
		return nil, unauthorized(err)
	}
	if t.Kind != kindAccess || !s.now().Before(t.Expires) {
		return nil, ErrUnauthorized
	}
	g, err := s.grant(ctx, t.GrantID)
	if err != nil {
		return nil, unauthorized(err)
	}
	var u User
	if err := s.Store.Get(ctx, store.Key{PK: "USER#" + g.UserID, SK: "META"}, &u); err != nil {
		return nil, unauthorized(err)
	}
	key, err := s.Sealer.Open(ctx, u.ID, u.KeyCiphertext)
	if err != nil {
		return nil, fmt.Errorf("open key of user %s: %w", u.ID, err)
	}
	return &Caller{UserID: u.ID, Username: u.Username, GrantID: g.ID, Key: string(key)}, nil
}

func unauthorized(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrUnauthorized
	}
	return err
}

// Challenge writes the 401 that tells an MCP client where to authorize.
func (s *Server) Challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, s.Issuer))
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
}

// MintAccessToken issues an access token for an existing grant without
// the code exchange, for tests. The token is returned once and stored
// hashed.
func (s *Server) MintAccessToken(ctx context.Context, grantID string) (string, error) {
	g, err := s.grant(ctx, grantID)
	if err != nil {
		return "", err
	}
	now := s.now()
	access := newToken()
	if err := s.Store.Put(ctx, token{Hash: hash(access), Kind: kindAccess, GrantID: g.ID, ClientID: g.ClientID, Issued: now, Expires: now.Add(AccessTokenTTL)}); err != nil {
		return "", err
	}
	return access, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -count=1 ./internal/oauth/ && go vet ./internal/oauth/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/oauth`.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth
git commit -m "Add connect, token exchange with rotation, revocation and authentication"
```

---

### Task 7: Web: connect page, routes, middleware, request log

**Files:**
- Create: `internal/reqlog/reqlog.go`, `internal/reqlog/reqlog_test.go`
- Create: `internal/web/server.go`, `internal/web/connect.go`
- Create: `internal/web/templates/layout.html`, `home.html`, `connect.html`, `error.html`
- Create: `internal/web/static/style.css`
- Test: `internal/web/web_test.go`

**Interfaces:**
- Consumes: `oauth.Server` and its handlers, `ParseAuthorize`, `Connect`, `RedirectError` (Tasks 5–6); `hardcover.Client.Me`, `hardcover.KeyURL`, `hardcover.Error`, `hardcover.ErrNoUser`, `hardcover.ErrQuery` (Task 4).
- Produces:
  - `reqlog.With(ctx) (context.Context, *Fields)`
  - `reqlog.From(ctx) *Fields`
  - `(*Fields).Add(attrs ...any)` and `(*Fields).Attrs() []any`, both nil-safe
  - `web.Server{OAuth *oauth.Server; Hardcover *hardcover.Client; MCP http.Handler; BaseURL string; Allowed []string; OriginVerify string; Log *slog.Logger}`
  - `(*Server).Handler() http.Handler`

- [ ] **Step 1: Write the failing tests**

`internal/reqlog/reqlog_test.go`:

```go
package reqlog

import (
	"context"
	"slices"
	"testing"
)

func TestFields(t *testing.T) {
	ctx, f := With(context.Background())
	From(ctx).Add("tool", "hardcover_query")
	From(ctx).Add("hc_status", 200)
	if got := f.Attrs(); !slices.Equal(got, []any{"tool", "hardcover_query", "hc_status", 200}) {
		t.Fatalf("attrs: %v", got)
	}
	// Without Fields in the context, Add and Attrs do nothing.
	From(context.Background()).Add("x", 1)
	if got := From(context.Background()).Attrs(); got != nil {
		t.Fatalf("nil fields: %v", got)
	}
}
```

`internal/web/web_test.go`:

```go
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
	oa := &oauth.Server{Store: st, Sealer: sealer.NewLocal(), Issuer: base, AllowedRedirectHosts: []string{"claude.ai", "claude.com"}}
	if mcp == nil {
		mcp = http.NotFoundHandler()
	}
	logs := &bytes.Buffer{}
	s := &Server{
		OAuth:     oa,
		Hardcover: &hardcover.Client{URL: fakeHardcover(t).URL},
		MCP:       mcp,
		BaseURL:   base,
		Allowed:   []string{"adam", "someone-else"},
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
	// Pasted with a Bearer prefix and spaces; the allowlist ignores case.
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
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/reqlog/ ./internal/web/`
Expected: FAIL to build, with `undefined: With`, `undefined: Server`, `undefined: msgNoKey`, and so on.

- [ ] **Step 3: Implement reqlog**

```go
// Package reqlog carries attributes from deep inside a request's handling
// (the MCP tools) back out to the one log line the access log writes for
// the request.
package reqlog

import (
	"context"
	"sync"
)

// Fields collects a request's extra log attributes, as slog key-value
// pairs. A nil *Fields ignores Add, so handlers need not check.
type Fields struct {
	mu    sync.Mutex
	attrs []any
}

type ctxKey struct{}

// With returns a context carrying a new, empty Fields.
func With(ctx context.Context) (context.Context, *Fields) {
	f := &Fields{}
	return context.WithValue(ctx, ctxKey{}, f), f
}

// From returns the context's Fields, or nil.
func From(ctx context.Context) *Fields {
	f, _ := ctx.Value(ctxKey{}).(*Fields)
	return f
}

func (f *Fields) Add(attrs ...any) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attrs = append(f.attrs, attrs...)
}

// Attrs returns a copy of what was added.
func (f *Fields) Attrs() []any {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any(nil), f.attrs...)
}
```

- [ ] **Step 4: Implement the web server**

`internal/web/server.go`:

```go
// Package web is Dustjacket's HTTP surface: the OAuth endpoints and the
// connect page where a person pastes their Hardcover key, /mcp, a home
// page, and the middleware every request passes through.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/reqlog"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Server struct {
	OAuth     *oauth.Server
	Hardcover *hardcover.Client
	MCP       http.Handler
	// BaseURL is the public origin, "https://dustjacket.rothman.tools".
	BaseURL string
	// Allowed are the Hardcover usernames that may connect.
	Allowed []string
	// OriginVerify, when set, must match the X-Origin-Verify header
	// CloudFront adds, so the raw function URL is not a back door.
	OriginVerify string
	Log          *slog.Logger

	tmpl map[string]*template.Template
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	s.parseTemplates()
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(static)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { s.render(w, http.StatusOK, "home", s.BaseURL) })

	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.OAuth.Metadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.OAuth.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.OAuth.ProtectedResourceMetadata)
	mux.HandleFunc("POST /oauth/register", s.OAuth.Register)
	mux.HandleFunc("POST /oauth/token", s.OAuth.Token)
	mux.HandleFunc("POST /oauth/revoke", s.OAuth.Revoke)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize", s.connect)
	mux.Handle("/mcp", s.MCP)

	return s.accessLog(s.recoverer(s.originVerify(s.headers(mux))))
}

// accessLog writes one line per request, with whatever the handlers added
// through reqlog. It logs the path and never the query, which carries
// OAuth codes, nor any body, which can carry a Hardcover key.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, fields := reqlog.With(r.Context())
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(ctx))
		attrs := []any{"method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds()}
		if m := r.Header.Get("Mcp-Method"); m != "" {
			attrs = append(attrs, "mcp_method", m)
		}
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" {
			attrs = append(attrs, "mcp_version", v)
		}
		s.log().Info("request", append(attrs, fields.Attrs()...)...)
	})
}

// statusWriter records the status a handler sends; one that never calls
// WriteHeader sends 200.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController (the MCP SDK flushes through one)
// reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log().Error("panic", "path", r.URL.Path, "err", rec)
				http.Error(w, "something went wrong", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originVerify(next http.Handler) http.Handler {
	if s.OriginVerify == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Origin-Verify") != s.OriginVerify {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// headers sets the security headers. The CSP has no form-action: the
// connect form's redirect to the client's callback would be subject to
// it, hop by hop, and the page has nothing a form could be injected into.
func (s *Server) headers(next http.Handler) http.Handler {
	const csp = "default-src 'none'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if strings.HasPrefix(s.BaseURL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// same-origin, not no-referrer: under no-referrer browsers send
		// "Origin: null" with the connect form, which the origin check
		// rejects. Cross-origin links (Hardcover's key page) get nothing.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// cacheStatic lets browsers keep the stylesheet for an hour.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Server) parseTemplates() {
	s.tmpl = map[string]*template.Template{}
	pages, _ := fs.Glob(templateFS, "templates/*.html")
	for _, p := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		s.tmpl[name] = template.Must(template.ParseFS(templateFS, "templates/layout.html", p))
	}
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.tmpl[name].ExecuteTemplate(w, "layout.html", data); err != nil {
		s.log().Error("render", "template", name, "err", err)
	}
}
```

`internal/web/connect.go`:

```go
package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
)

// What the connect page says when a key does not work out.
const (
	msgNoKey       = "Paste your Hardcover API key."
	msgBadKey      = "Hardcover didn't accept that key. Check that you copied all of it and that it hasn't expired."
	msgNoScope     = "That key can't read your Hardcover profile. Create one with the link above."
	msgUnavailable = "Couldn't reach Hardcover. Try again in a moment."
)

type connectView struct {
	ClientName string
	Query      string // the authorize query, re-validated on post
	KeyURL     string
	Message    string
}

// authorize shows the connect page for a valid authorize request.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	req, err := s.OAuth.ParseAuthorize(r)
	if err != nil {
		s.authorizeError(w, r, req, err)
		return
	}
	s.render(w, http.StatusOK, "connect", connectView{ClientName: req.Client.Name, Query: r.URL.RawQuery, KeyURL: hardcover.KeyURL})
}

func (s *Server) authorizeError(w http.ResponseWriter, r *http.Request, req *oauth.AuthorizeRequest, err error) {
	var re *oauth.RedirectError
	if errors.As(err, &re) && req != nil {
		req.Redirect(w, r, map[string]string{"error": re.Code, "error_description": re.Desc})
		return
	}
	s.render(w, http.StatusBadRequest, "error", "This connect link isn't valid: "+err.Error()+". Start again from Claude.")
}

// connect takes the posted key: asks Hardcover whose it is, checks the
// allowlist, records the connection and sends the client its code.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		s.render(w, http.StatusForbidden, "error", "This form has to be sent from Dustjacket's own page.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "error", "That form didn't arrive intact. Start again from Claude.")
		return
	}
	// The original query rides along in the form so the request is
	// re-validated rather than trusted.
	q := r.PostFormValue("q")
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q
	req, err := s.OAuth.ParseAuthorize(r2)
	if err != nil {
		s.authorizeError(w, r, req, err)
		return
	}
	if r.PostFormValue("decision") == "cancel" {
		req.Redirect(w, r, map[string]string{"error": "access_denied"})
		return
	}
	view := connectView{ClientName: req.Client.Name, Query: q, KeyURL: hardcover.KeyURL}
	key := normalizeKey(r.PostFormValue("key"))
	if key == "" {
		view.Message = msgNoKey
		s.render(w, http.StatusBadRequest, "connect", view)
		return
	}
	user, err := s.Hardcover.Me(r.Context(), key)
	if err != nil {
		status, msg := meFailure(err)
		s.log().Warn("connect: key rejected", "err", err)
		view.Message = msg
		s.render(w, status, "connect", view)
		return
	}
	if !s.allowed(user.Username) {
		s.log().Warn("connect: not on the allowlist", "username", user.Username)
		view.Message = "@" + user.Username + " isn't on this server's allowlist. Ask Adam to add you."
		s.render(w, http.StatusForbidden, "connect", view)
		return
	}
	code, err := s.OAuth.Connect(r.Context(), req, strconv.Itoa(user.ID), user.Username, key)
	if err != nil {
		s.log().Error("connect", "err", err)
		s.render(w, http.StatusInternalServerError, "error", "Something went wrong saving the connection. Try again from Claude.")
		return
	}
	s.log().Info("connected", "username", user.Username, "client", req.Client.ID)
	req.Redirect(w, r, map[string]string{"code": code})
}

// meFailure says what the connect page tells the person when Hardcover's
// `me` fails for their key.
func meFailure(err error) (int, string) {
	var he *hardcover.Error
	switch {
	case errors.Is(err, hardcover.ErrNoUser):
		return http.StatusBadRequest, msgBadKey
	case errors.Is(err, hardcover.ErrQuery):
		return http.StatusBadRequest, msgNoScope
	case !errors.As(err, &he):
		return http.StatusBadGateway, msgUnavailable
	case he.Status == http.StatusForbidden && he.Code == "insufficient_scope":
		return http.StatusBadRequest, msgNoScope
	case he.Retryable() || he.Status == http.StatusTooManyRequests:
		return http.StatusBadGateway, msgUnavailable
	}
	return http.StatusBadRequest, msgBadKey
}

// sameOrigin requires the form to have been posted from our own page.
// Browsers send Origin with every form post.
func (s *Server) sameOrigin(r *http.Request) bool {
	u, err := url.Parse(s.BaseURL)
	return err == nil && r.Header.Get("Origin") == u.Scheme+"://"+u.Host
}

func (s *Server) allowed(username string) bool {
	for _, a := range s.Allowed {
		if strings.EqualFold(strings.TrimSpace(a), username) {
			return true
		}
	}
	return false
}

// normalizeKey trims the pasted key and drops a "Bearer " prefix, in case
// it was copied with one.
func normalizeKey(k string) string {
	k = strings.TrimSpace(k)
	if len(k) > 7 && strings.EqualFold(k[:7], "bearer ") {
		k = strings.TrimSpace(k[7:])
	}
	return k
}
```

`internal/web/templates/layout.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>{{template "title" .}} · Dustjacket</title>
<link rel="stylesheet" href="/static/style.css">
<link rel="icon" href="data:,">
</head>
<body>
<main>
{{template "content" .}}
</main>
</body>
</html>
```

`internal/web/templates/home.html`:

```html
{{define "title"}}Dustjacket{{end}}
{{define "content"}}
<h1>Dustjacket</h1>
<p>Dustjacket connects Claude to your <a href="https://hardcover.app">Hardcover</a> account. It's a small personal server for a few people and isn't affiliated with Hardcover.</p>
<p>To use it, add a custom connector in Claude with this URL:</p>
<p><code>{{.}}/mcp</code></p>
{{end}}
```

`internal/web/templates/connect.html`:

```html
{{define "title"}}Connect Hardcover{{end}}
{{define "content"}}
<h1>Connect Claude to Hardcover</h1>
<p><strong>{{.ClientName}}</strong> wants to use your Hardcover account through Dustjacket, a small personal server that isn't affiliated with Hardcover.</p>
{{if .Message}}<p class="message" role="alert">{{.Message}}</p>{{end}}
<form method="post" action="/oauth/authorize">
  <input type="hidden" name="q" value="{{.Query}}">
  <ol>
    <li><a href="{{.KeyURL}}" target="_blank" rel="noopener noreferrer">Create a Hardcover API key</a>. The permissions Dustjacket needs are already selected.</li>
    <li><label for="key">Paste the key here:</label>
      <input id="key" name="key" type="password" autocomplete="off" spellcheck="false" required></li>
  </ol>
  <p class="actions">
    <button type="submit" name="decision" value="connect">Connect</button>
    <button type="submit" name="decision" value="cancel" formnovalidate>Cancel</button>
  </p>
</form>
<p class="small">Dustjacket keeps your key encrypted and uses it only for what you ask Claude to do. To cut it off, delete the key on Hardcover or remove the connector in Claude.</p>
{{end}}
```

`internal/web/templates/error.html`:

```html
{{define "title"}}Something's wrong{{end}}
{{define "content"}}
<h1>Something's wrong</h1>
<p>{{.}}</p>
{{end}}
```

`internal/web/static/style.css`:

```css
:root {
  color-scheme: light dark;
  --text: #1f1d1a;
  --muted: #6b665e;
  --bg: #fbf8f3;
  --accent: #7a3e1d;
  --alert: #9b1c1c;
}

@media (prefers-color-scheme: dark) {
  :root {
    --text: #ece7df;
    --muted: #a39d93;
    --bg: #1b1916;
    --accent: #e0a37e;
    --alert: #f19999;
  }
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: 17px/1.5 system-ui, sans-serif;
}

main {
  max-width: 34rem;
  margin: 0 auto;
  padding: 2rem 1rem;
}

a { color: var(--accent); }
code { overflow-wrap: anywhere; }
.small { color: var(--muted); font-size: 0.9em; }
.message { color: var(--alert); font-weight: 600; }
li { margin-bottom: 1rem; }
label { display: block; }

input[type="password"] {
  box-sizing: border-box;
  width: 100%;
  margin-top: 0.4rem;
  padding: 0.6rem;
  font: inherit;
}

.actions { display: flex; gap: 0.75rem; }

button {
  padding: 0.6rem 1.2rem;
  font: inherit;
  cursor: pointer;
}

button[value="connect"] {
  background: var(--accent);
  color: var(--bg);
  border: 0;
  border-radius: 4px;
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 ./internal/reqlog/ ./internal/web/ && go vet ./internal/...`
Expected: `ok` for both packages.

- [ ] **Step 6: Commit**

```bash
git add internal/reqlog internal/web
git commit -m "Add the connect page, routes, security headers and the request log"
```

---

### Task 8: Reference: schema snapshot and lookups

**Files:**
- Create: `scripts/update-schema.sh`
- Create: `internal/reference/schema.graphql`, `internal/reference/SCHEMA_LICENSE.md` (both fetched by the script)
- Create: `internal/reference/reference.go`
- Test: `internal/reference/lookup_test.go`

**Interfaces:**
- Produces:
  - `reference.Schema *ast.Schema`
  - `reference.Lookup(name string) (string, error)`

- [ ] **Step 1: Add the script and fetch the snapshot**

`scripts/update-schema.sh`:

```sh
#!/bin/sh
# Refreshes the schema snapshot hardcover_docs serves from Hardcover's
# docs repository (MIT; its license travels with the file). Run from the
# repository root, then `go test ./internal/reference/`: it fails if a
# guide's examples no longer validate against the new schema.
set -eu
base=https://raw.githubusercontent.com/hardcoverapp/hardcover-docs/main
curl -fsSL "$base/schema.graphql" -o internal/reference/schema.graphql
curl -fsSL "$base/LICENSE.md" -o internal/reference/SCHEMA_LICENSE.md
```

```bash
chmod +x scripts/update-schema.sh && ./scripts/update-schema.sh && wc -c internal/reference/schema.graphql
go get github.com/vektah/gqlparser/v2@v2.5.58 github.com/agnivade/levenshtein@v1.2.1
```

Expected: `schema.graphql` is roughly 580 KB (585,764 bytes on 2026-09-23).

- [ ] **Step 2: Write the failing test**

```go
package reference

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		// A table: its object type and its query root field.
		{"user_books", []string{"type user_books {\n", "  status_id: Int!\n", "query_root.user_books(distinct_on: [user_books_select_column!], limit: Int, offset: Int, order_by: [user_books_order_by!], where: user_books_bool_exp): [user_books!]!"}},
		{"mutation_root.update_user_book", []string{"mutation_root.update_user_book(id: Int!, object: UserBookUpdateInput!): UserBookIdType"}},
		{"update_user_book", []string{"mutation_root.update_user_book(id: Int!, object: UserBookUpdateInput!): UserBookIdType"}},
		{"UserBookUpdateInput", []string{"input UserBookUpdateInput {\n", "  status_id: Int\n", "  rating: numeric\n"}},
		{"TrendingDuration", []string{"enum TrendingDuration {\n", "  month\n"}},
		{"citext", []string{"scalar citext"}},
		{" query_root.me ", []string{"query_root.me(distinct_on: [users_select_column!], limit: Int, offset: Int, order_by: [users_order_by!], where: users_bool_exp): [users!]!"}},
	} {
		got, err := Lookup(tc.name)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: lacks %q in:\n%s", tc.name, w, got)
			}
		}
		if strings.Contains(got, `"""`) {
			t.Errorf("%s: descriptions leaked in", tc.name)
		}
	}
}

func TestLookupSuggests(t *testing.T) {
	for name, want := range map[string]string{
		"UserBooks":                  "user_books",
		"query_root.user_book":       "user_books",
		"mutation_root.insert_bookz": "insert_book",
	} {
		_, err := Lookup(name)
		if err == nil || !strings.Contains(err.Error(), "did you mean") || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Lookup("users.id"); err == nil || !strings.Contains(err.Error(), "not query_root or mutation_root") {
		t.Errorf("users.id: %v", err)
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/reference/`
Expected: FAIL to build, with `undefined: Lookup`.

- [ ] **Step 4: Implement**

```go
// Package reference is what hardcover_docs serves: guides written for
// Claude (guides.go), and definitions looked up in a snapshot of
// Hardcover's GraphQL schema (from hardcoverapp/hardcover-docs, MIT;
// refreshed by scripts/update-schema.sh).
package reference

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/agnivade/levenshtein"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

//go:embed schema.graphql
var schemaSDL string

// Schema is Hardcover's schema as of the snapshot.
var Schema = gqlparser.MustLoadSchema(&ast.Source{Name: "schema.graphql", Input: schemaSDL})

// Lookup returns the definition of every type and root field with this
// name: Hasura often gives a table's object type and its query root field
// the same name. "query_root.<field>" or "mutation_root.<field>" narrows
// it to one root field. Unknown names get suggestions.
func Lookup(name string) (string, error) {
	name = strings.TrimSpace(name)
	if root, field, ok := strings.Cut(name, "."); ok {
		def := Schema.Types[root]
		if def == nil || (def != Schema.Query && def != Schema.Mutation) {
			return "", fmt.Errorf("%q is not query_root or mutation_root", root)
		}
		if f := def.Fields.ForName(field); f != nil {
			return root + "." + signature(f), nil
		}
		return "", notFound(name, fieldNames(def))
	}
	var parts []string
	if d := Schema.Types[name]; d != nil {
		parts = append(parts, printType(d))
	}
	for _, def := range []*ast.Definition{Schema.Query, Schema.Mutation} {
		if f := def.Fields.ForName(name); f != nil {
			parts = append(parts, def.Name+"."+signature(f))
		}
	}
	if len(parts) == 0 {
		return "", notFound(name, allNames())
	}
	return strings.Join(parts, "\n\n"), nil
}

// printType renders a definition as compact SDL: one line per field,
// arguments inline, no descriptions (Hasura's are boilerplate).
func printType(d *ast.Definition) string {
	var b strings.Builder
	switch d.Kind {
	case ast.Scalar:
		fmt.Fprintf(&b, "scalar %s", d.Name)
	case ast.Union:
		fmt.Fprintf(&b, "union %s = %s", d.Name, strings.Join(d.Types, " | "))
	case ast.Enum:
		fmt.Fprintf(&b, "enum %s {\n", d.Name)
		for _, v := range d.EnumValues {
			fmt.Fprintf(&b, "  %s\n", v.Name)
		}
		b.WriteString("}")
	default:
		keyword := map[ast.DefinitionKind]string{ast.Object: "type", ast.Interface: "interface", ast.InputObject: "input"}[d.Kind]
		fmt.Fprintf(&b, "%s %s {\n", keyword, d.Name)
		for _, f := range d.Fields {
			if !strings.HasPrefix(f.Name, "__") {
				fmt.Fprintf(&b, "  %s\n", signature(f))
			}
		}
		b.WriteString("}")
	}
	return b.String()
}

func signature(f *ast.FieldDefinition) string {
	if len(f.Arguments) == 0 {
		return f.Name + ": " + f.Type.String()
	}
	args := make([]string, len(f.Arguments))
	for i, a := range f.Arguments {
		args[i] = a.Name + ": " + a.Type.String()
	}
	return f.Name + "(" + strings.Join(args, ", ") + "): " + f.Type.String()
}

func fieldNames(d *ast.Definition) []string {
	var out []string
	for _, f := range d.Fields {
		if !strings.HasPrefix(f.Name, "__") {
			out = append(out, f.Name)
		}
	}
	return out
}

// allNames is every type and root field name, once each.
func allNames() []string {
	seen := map[string]bool{}
	for name := range Schema.Types {
		if !strings.HasPrefix(name, "__") {
			seen[name] = true
		}
	}
	for _, def := range []*ast.Definition{Schema.Query, Schema.Mutation} {
		for _, n := range fieldNames(def) {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// notFound suggests the five closest names, ignoring case.
func notFound(name string, candidates []string) error {
	type scored struct {
		name string
		d    int
	}
	lower := strings.ToLower(name)
	s := make([]scored, len(candidates))
	for i, c := range candidates {
		s[i] = scored{c, levenshtein.ComputeDistance(lower, strings.ToLower(c))}
	}
	sort.Slice(s, func(i, j int) bool {
		if s[i].d != s[j].d {
			return s[i].d < s[j].d
		}
		return s[i].name < s[j].name
	})
	names := make([]string, 0, 5)
	for i := 0; i < len(s) && i < 5; i++ {
		names = append(names, s[i].name)
	}
	return fmt.Errorf("nothing is named %q; did you mean %s?", name, strings.Join(names, ", "))
}
```

- [ ] **Step 5: Run the tests**

Run: `go mod tidy && go test -count=1 ./internal/reference/ && go vet ./internal/reference/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/reference`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum scripts internal/reference
git commit -m "Add the schema snapshot and type and root-field lookups"
```

---

### Task 9: Guides

These are the guides Claude reads through `hardcover_docs`. Every GraphQL example in them was validated against the snapshot while this plan was written. The places marked `<!-- L… -->` and `<!-- TRAPS -->` get text chosen from Task 1's findings. The test fails while any marker remains.

**Files:**
- Create: `internal/reference/guides.go`
- Create: `internal/reference/guides/overview.md`, `library.md`, `reading.md`, `search.md`, `catalog.md`, `lists.md`, `goals.md`, `journal.md`, `social.md`, `recommendations.md`, `limits.md`
- Test: `internal/reference/guides_test.go`
- Read: `docs/hardcover-notes.md`

**Interfaces:**
- Consumes: `reference.Schema` (Task 8).
- Produces:
  - `reference.Topics []string`
  - `reference.Overview() string`
  - `reference.Guide(topic string) (string, error)`

- [ ] **Step 1: Write the failing test**

````go
package reference

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
)

var graphqlBlock = regexp.MustCompile("(?s)```graphql\n(.*?)```")

func TestGuidesMatchTopics(t *testing.T) {
	files, err := fs.Glob(guides, "guides/*.md")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(f, "guides/"), ".md"))
	}
	want := append([]string{"overview"}, Topics...)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("guide files %v, want %v", names, want)
	}
	overview := Overview()
	for _, topic := range Topics {
		if !strings.Contains(overview, "- `"+topic+"`: ") {
			t.Errorf("the overview does not list %s", topic)
		}
		if _, err := Guide(strings.ToUpper(topic)); err != nil {
			t.Error(err)
		}
	}
	if _, err := Guide("nope"); err == nil || !strings.Contains(err.Error(), "library, reading") {
		t.Errorf("unknown topic: %v", err)
	}
}

// Every example in every guide must be a valid operation against the
// schema snapshot, so a refresh that breaks one fails here.
func TestGuideExamplesValidate(t *testing.T) {
	for _, name := range append([]string{"overview"}, Topics...) {
		b, err := guides.ReadFile("guides/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if strings.Contains(text, "<!--") {
			t.Errorf("%s: a live-check marker is still unfilled", name)
		}
		for i, m := range graphqlBlock.FindAllStringSubmatch(text, -1) {
			if _, errs := gqlparser.LoadQuery(Schema, m[1]); errs != nil {
				t.Errorf("%s example %d: %v", name, i+1, errs)
			}
		}
	}
}
````

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/reference/`
Expected: FAIL to build, with `undefined: guides`, `undefined: Topics` and `undefined: Overview`.

- [ ] **Step 3: Implement the loader**

```go
package reference

import (
	"embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed guides/*.md
var guides embed.FS

// Topics are the guides besides the overview, in the order the overview
// lists them.
var Topics = []string{"library", "reading", "search", "catalog", "lists", "goals", "journal", "social", "recommendations", "limits"}

// Overview is where Claude starts: the tools, the ids, the topics, the
// traps.
func Overview() string {
	b, err := guides.ReadFile("guides/overview.md")
	if err != nil {
		panic(err) // embedded; the tests read every guide
	}
	return string(b)
}

// Guide returns one topic's guide.
func Guide(topic string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(topic))
	if !slices.Contains(Topics, t) {
		return "", fmt.Errorf("there is no guide %q; the topics are %s", topic, strings.Join(Topics, ", "))
	}
	b, err := guides.ReadFile("guides/" + t + ".md")
	return string(b), err
}
```

- [ ] **Step 4: Write the guides, markers included**

`internal/reference/guides/overview.md`:

~~~markdown
# Dustjacket: the Hardcover API

Dustjacket runs Hardcover's GraphQL API as the connected person, with their
own key.

- `hardcover_query` runs a query (reads).
- `hardcover_mutate` runs a mutation (writes). Read the topic's guide first,
  and tell the person what will change.
- `hardcover_docs` returns this overview, a guide (`topic`), or a type's or
  root field's definition (`name`, e.g. `user_books`, `UserBookUpdateInput`,
  `mutation_root.update_user_book`).

## Start here

`me` returns a list with one element, the connected person. Get their id
once and reuse it: most queries filter on `user_id`.

```graphql
query Me {
  me { id username books_count }
}
```

## IDs

- Reading status (`status_id`): 1 Want to Read, 2 Currently Reading,
  3 Read, 4 Paused, 5 Did Not Finish, 6 Ignored.
- Privacy (`privacy_setting_id`): 1 Public, 2 Followers, 3 Private.

## Topics

- `library`: the person's books, statuses, ratings, reviews, owned editions
- `reading`: reads, progress, start and finish dates, reading history
- `search`: finding books, authors, series and users by name
- `catalog`: books, editions, authors, series
- `lists`: the person's lists
- `goals`: reading goals
- `journal`: notes and quotes
- `social`: who they follow and what those people are reading
- `recommendations`: suggesting books from their history
- `limits`: rate limits, request limits, disabled operators

## Traps

- Names match only through `search`; `_like` and `_ilike` are disabled.
- A request may have at most 5 top-level fields, or 1 `search` alone.
- Mutations that update take the row's own id (`user_books.id`,
  `user_book_reads.id`, `list_books.id`), not the book id.
<!-- TRAPS -->
~~~

`internal/reference/guides/library.md`:

~~~markdown
# Library and statuses

A person's library is their `user_books`: one row per book they have saved,
with a reading status, an optional rating and review, and private notes.

Status IDs (`status_id`): 1 Want to Read, 2 Currently Reading, 3 Read,
4 Paused, 5 Did Not Finish, 6 Ignored.

Always filter on `user_id` with the id from `me` (see the overview).

## Books by status

```graphql
query Shelf($me: Int!, $status: Int!) {
  user_books(
    where: {user_id: {_eq: $me}, status_id: {_eq: $status}}
    order_by: {date_added: desc}
    limit: 25
  ) {
    id
    status_id
    rating
    date_added
    last_read_date
    book { id title slug contributions { author { name } } }
  }
}
```

## Counts

```graphql
query Counts($me: Int!) {
  read: user_books_aggregate(where: {user_id: {_eq: $me}, status_id: {_eq: 3}}) {
    aggregate { count avg { rating } }
  }
  tbr: user_books_aggregate(where: {user_id: {_eq: $me}, status_id: {_eq: 1}}) {
    aggregate { count }
  }
}
```

## Is a book already in the library?

```graphql
query InLibrary($me: Int!, $book: Int!) {
  user_books(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) {
    id
    status_id
    rating
  }
}
```

## Add a book

Find its `book_id` with `search` first (see the search guide).

```graphql
mutation Add($book: Int!) {
  insert_user_book(object: {book_id: $book, status_id: 1}) {
    id
    error
    user_book { id status_id }
  }
}
```

## Change status, rating or review

`update_user_book` takes the `user_books.id`, not the book id.
`rating` is 0.5 to 5. `review_markdown` holds a review's text.

<!-- L1 -->

```graphql
mutation Update($id: Int!, $object: UserBookUpdateInput!) {
  update_user_book(id: $id, object: $object) {
    error
    user_book { id status_id rating review_markdown private_notes }
  }
}
```

Marking a book read also needs its dates on a read; see the reading guide.

## Remove a book

```graphql
mutation Remove($id: Int!) {
  delete_user_book(id: $id) { id }
}
```

## Owned editions

`edition_owned(id: <edition id>)` marks an edition as owned.

<!-- L2 -->
~~~

`internal/reference/guides/reading.md`:

~~~markdown
# Reading progress and dates

Each time a person reads a book is a row in `user_book_reads`, attached to
the library entry (`user_book_id`): `started_at`, `finished_at` (dates,
`YYYY-MM-DD`), and progress as `progress_pages` or `progress_seconds`.
A read with no `finished_at` is the open one.

## The open read

```graphql
query OpenRead($ub: Int!) {
  user_book_reads(where: {user_book_id: {_eq: $ub}, finished_at: {_is_null: true}}) {
    id
    started_at
    progress_pages
    progress_seconds
    edition { id pages audio_seconds }
  }
}
```

## Start reading

Set the library entry to status 2, then add a read. If the book is not in
the library yet, `insert_user_book` with `status_id: 2` first.

```graphql
mutation Start($ub: Int!, $today: date!) {
  insert_user_book_read(user_book_id: $ub, user_book_read: {started_at: $today}) {
    id
    error
    user_book_read { id started_at }
  }
}
```

Only add a read when there is no open one; otherwise update the open one.

## Update progress

<!-- L1-read -->

```graphql
mutation Progress($id: Int!, $read: DatesReadInput!) {
  update_user_book_read(id: $id, object: $read) {
    error
    user_book_read { id started_at progress_pages progress_seconds }
  }
}
```

<!-- L7 -->

## Finish

Set `finished_at` on the open read, and set the library entry to status 3
(and a rating, if the person gave one) with `update_user_book`.

## What was read, and when

```graphql
query ReadIn($me: Int!, $from: date!, $to: date!) {
  user_book_reads(
    where: {user_book: {user_id: {_eq: $me}}, finished_at: {_gte: $from, _lte: $to}}
    order_by: {finished_at: asc}
  ) {
    finished_at
    user_book { rating book { id title pages } }
  }
}
```
~~~

`internal/reference/guides/search.md`:

~~~markdown
# Search

Text matching works only through `search`: `_like`, `_ilike` and the regex
operators are disabled, so `where: {title: {_ilike: ...}}` fails. A request
may contain one `search` and nothing else at the top level, and search has a
2-second timeout.

`query_type` is one of `book` (the default), `author`, `series`, `list`,
`user`, `character`, `publisher`, `prompt` (any case). `per_page` defaults
to 25; ask for 5 to 10.

```graphql
query FindBook($q: String!) {
  search(query: $q, query_type: "book", per_page: 5) {
    ids
    results
  }
}
```

`ids` are the matches' ids in rank order. `results` is the search engine's
raw JSON.

<!-- L4 -->

To get more than `results` carries, take `ids` and fetch those rows in a
second request. `_in` does not keep order, so re-sort by the position in
`ids`:

```graphql
query Hydrate($ids: [Int!]!) {
  books(where: {id: {_in: $ids}}) {
    id
    title
    release_year
    pages
    rating
    contributions { author { name } }
    book_series { position series { name } }
  }
}
```

Searching authors, series or users works the same way; hydrate with
`authors`, `series` or `users`.

Book search looks in `title`, `isbns`, `series_names`, `author_names` and
`alternative_titles` by default. To search other fields, pass `fields`
with one weight each, e.g. `fields: "genres", weights: "1"`.
~~~

`internal/reference/guides/catalog.md`:

~~~markdown
# Catalog: books, editions, authors, series

A book is the work; editions are its printings, ebooks and audiobooks.
Library entries, lists and reads point at books, and optionally an edition.

## A book

```graphql
query Book($id: Int!) {
  books_by_pk(id: $id) {
    id
    title
    subtitle
    slug
    release_year
    pages
    rating
    ratings_count
    description
    cached_tags
    contributions { contribution author { id name } }
    book_series { position series { id name } }
    default_physical_edition { id pages isbn_13 }
    default_audio_edition { id audio_seconds }
  }
}
```

`cached_tags` holds the book's top genres, moods and tags.

## An edition by ISBN

```graphql
query ByIsbn($isbn: String!) {
  editions(where: {isbn_13: {_eq: $isbn}}) {
    id
    title
    edition_format
    pages
    book { id title }
  }
}
```

Use `isbn_10` for ten-digit ISBNs and `asin` for Kindle and Audible ids.

## An author's best-known books

```graphql
query Author($id: Int!) {
  authors_by_pk(id: $id) {
    name
    books_count
    contributions(
      where: {contributable_type: {_eq: "Book"}}
      order_by: {book: {users_count: desc}}
      limit: 20
    ) {
      contribution
      book { id title release_year }
    }
  }
}
```

## The books in a series, in order

The catalog has duplicate and partial books; this filter keeps one book per
position:

```graphql
query Series($id: Int!) {
  book_series(
    where: {series_id: {_eq: $id}, book: {canonical_id: {_is_null: true}, is_partial_book: {_eq: false}}}
    order_by: {position: asc}
    distinct_on: position
  ) {
    position
    book { id title release_year }
  }
}
```
~~~

`internal/reference/guides/lists.md`:

~~~markdown
# Lists

Privacy IDs (`privacy_setting_id`): 1 Public, 2 Followers, 3 Private.

## My lists

```graphql
query MyLists($me: Int!) {
  lists(where: {user_id: {_eq: $me}}, order_by: {updated_at: desc}) {
    id
    name
    slug
    books_count
    privacy_setting_id
    ranked
  }
}
```

## A list's books

```graphql
query ListBooks($list: Int!) {
  list_books(where: {list_id: {_eq: $list}}, order_by: {position: asc}) {
    id
    position
    book { id title contributions { author { name } } }
  }
}
```

## Create a list

```graphql
mutation NewList($name: String!, $description: String) {
  insert_list(object: {name: $name, description: $description, privacy_setting_id: 1, ranked: false}) {
    id
    errors
    list { id slug }
  }
}
```

## Add and remove books

```graphql
mutation AddToList($list: Int!, $book: Int!) {
  insert_list_book(object: {list_id: $list, book_id: $book}) {
    id
    list_book { id position }
  }
}
```

`delete_list_book(id: ...)` takes the `list_books.id`, not the book id.

```graphql
mutation RemoveFromList($id: Int!) {
  delete_list_book(id: $id) { id }
}
```

`update_list(id:, object:)` renames or re-describes a list;
`delete_list(id:)` deletes it. Confirm with the person before deleting.
~~~

`internal/reference/guides/goals.md`:

~~~markdown
# Reading goals

```graphql
query Goals($me: Int!) {
  goals(where: {user_id: {_eq: $me}, archived: {_eq: false}}, order_by: {end_date: desc}) {
    id
    description
    metric
    goal
    progress
    start_date
    end_date
    state
    completed_at
  }
}
```

`progress` is how far along the goal is, in its `metric`.

## Create a goal

The values `metric` takes are not documented; read an existing goal's
`metric` first and reuse it. `conditions` narrows what counts (formats,
categories); `{}` counts everything.

```graphql
mutation NewGoal($goal: Int!, $metric: String!, $description: String!, $start: date!, $end: date!) {
  insert_goal(object: {goal: $goal, metric: $metric, description: $description, start_date: $start, end_date: $end, conditions: {}, privacy_setting_id: 1}) {
    id
    errors
    goal { id }
  }
}
```
~~~

`internal/reference/guides/journal.md`:

~~~markdown
# Reading journal

Journal entries are notes and quotes about a book, plus events Hardcover
records itself (`status_read`, `rated`, `progress_updated`, ...).

```graphql
query Journal($me: Int!) {
  reading_journals(where: {user_id: {_eq: $me}}, order_by: {action_at: desc}, limit: 20) {
    id
    event
    entry
    action_at
    privacy_setting_id
    book { id title }
  }
}
```

## Add a note or a quote

`event` is `note` or `quote`. `privacy_setting_id` and `tags` are required.

<!-- L8 -->

```graphql
mutation Note($book: Int!, $entry: String!) {
  insert_reading_journal(object: {book_id: $book, event: "note", entry: $entry, privacy_setting_id: 1, tags: []}) {
    id
    errors
    reading_journal { id }
  }
}
```

`delete_reading_journal(id:)` removes an entry.
~~~

`internal/reference/guides/social.md`:

~~~markdown
# Friends and activity

## Who I follow

```graphql
query Following($me: Int!) {
  followed_users(where: {user_id: {_eq: $me}}) {
    followed_user { id username name books_count }
  }
}
```

## What they have been reading

Activities are status changes, ratings and reviews. Pass the ids from above:

```graphql
query FriendsActivity($ids: [Int!]!) {
  activities(where: {user_id: {_in: $ids}}, order_by: {created_at: desc}, limit: 25) {
    event
    created_at
    user { username }
    book { id title }
  }
}
```

## Someone's public profile

```graphql
query Profile($username: citext!) {
  users(where: {username: {_eq: $username}}) {
    id
    username
    name
    bio
    books_count
    followers_count
  }
}
```

Dustjacket's key cannot follow, like or block anyone.
~~~

`internal/reference/guides/recommendations.md`:

~~~markdown
# Recommendations

Hardcover has no recommendation query. Build one from the person's history:

1. **Taste.** Their highest-rated reads, with the books' `cached_tags`
   (genres, moods) and `cached_similar_book_ids`; also what they did not
   finish (status 5) and what is already on their TBR (status 1).

   ```graphql
   query Taste($me: Int!) {
     user_books(
       where: {user_id: {_eq: $me}, status_id: {_eq: 3}, rating: {_gte: 4}}
       order_by: {rating: desc}
       limit: 30
     ) {
       rating
       book { id title cached_tags cached_similar_book_ids contributions { author { id name } } }
     }
   }
   ```

2. **Candidates.** Any of: similar books (`cached_similar_book_ids`, then
   hydrate with `books`), other books by loved authors (catalog guide),
   the next book in their series (catalog guide), what friends rated highly
   (social guide), or what is trending:

   ```graphql
   query Trending {
     books_trending(duration: month, limit: 20) {
       ids
       error
     }
   }
   ```

3. **Filter.** Drop anything already in their library:

   ```graphql
   query Known($me: Int!, $ids: [Int!]!) {
     user_books(where: {user_id: {_eq: $me}, book_id: {_in: $ids}}) {
       book_id
       status_id
     }
   }
   ```

4. **Explain.** Say why each pick fits, in terms of books they loved.

Each step is a request against the 60-a-minute limit; keep candidate lists
to a few dozen ids.
~~~

`internal/reference/guides/limits.md`:

~~~markdown
# Limits

- **Rate:** 60 requests a minute, and 5,000 a day (50,000 for Hardcover
  supporters). Every top-level field in a request counts as one request.
  Each GraphQL result carries a `rate_limit` value with what remains.
- **Per request:** at most 5 top-level queries or 5 top-level mutations,
  or a single `search` on its own.
- **Time:** 30 seconds per query, 2 seconds for `search`. Dustjacket gives
  up after 20 seconds.
- **Size:** Dustjacket refuses results over 100 KB. Ask for the fields you
  need, and page with `limit` and `offset` (use `order_by` so pages are
  stable).
- **Operators:** `_like`, `_nlike`, `_ilike`, `_regex`, `_iregex`,
  `_nregex`, `_niregex`, `_similar` and `_nsimilar` are disabled. Match text
  with `search`.
- **Errors:** a 429 says how many seconds to wait; wait before retrying.
  A missing-scope error names the scope the person's key lacks.
~~~

- [ ] **Step 5: Run the test and see only the markers fail**

Run: `go test -count=1 ./internal/reference/`
Expected: FAIL with `a live-check marker is still unfilled` for overview, library, reading, search and journal, and nothing else. No example fails to validate.

- [ ] **Step 6: Replace each marker from the findings**

Replace each marker with the text for what `docs/hardcover-notes.md` recorded. When the finding is the "no trap" case, delete the marker and the blank line after it.

**`library.md`, `<!-- L1 -->`:**
- If the fields were kept:

  ```
  Send only the fields that change; the others keep their values.
  ```

- If they were nulled:

  ````
  `update_user_book` clears every field you leave out. Read the entry
  first and send its current values along with your change:

  ```graphql
  query Entry($id: Int!) {
    user_books_by_pk(id: $id) {
      status_id
      rating
      review_markdown
      review_has_spoilers
      private_notes
      privacy_setting_id
      edition_id
      date_added
      first_started_reading_date
      last_read_date
      read_count
      recommended_by
      recommended_for
      url
      media_url
    }
  }
  ```
  ````

**`library.md`, `<!-- L2 -->`:**
- If it is a toggle:

  ```
  It is a toggle: calling it on an edition already owned un-owns it.
  Check `owned` on the library entry first, and call it only to change it.
  ```

- If not: `Calling it again leaves the edition owned.`

**`reading.md`, `<!-- L1-read -->`:**
- If kept: ``Send only what changes, e.g. `{progress_pages: 150}`.``
- If nulled:

  ```
  `update_user_book_read` clears every field you leave out: send
  `started_at` (and `edition_id`, if the read has one) with the new progress,
  e.g. `{started_at: "2026-09-01", progress_pages: 150}`.
  ```

**`reading.md`, `<!-- L7 -->`:**
- If it persisted: ``Use `progress_pages` for print and ebooks, `progress_seconds` for audiobooks.``
- If not:

  ```
  `progress_seconds` is kept only on a read whose `edition_id` is an
  audiobook edition: set `edition_id` (the book's `default_audio_edition.id`)
  on the read. For print and ebooks, use `progress_pages`.
  ```

**`search.md`, `<!-- L4 -->`:** one or two sentences stating what L4 recorded. Say where each match's fields are, and which fields a book match carries that matter here (its ID, title, authors, year, series, popularity). For example, if that is what was recorded:

```
Each match is in `results.hits[].document`; a book's carries `id`,
`title`, `author_names`, `release_year`, `series_names` and `users_count`.
```

**`journal.md`, `<!-- L8 -->`:**
- If 0 fails silently:

  ```
  `privacy_setting_id: 0` fails without an error (the result is empty);
  use 1, 2 or 3.
  ```

- If it errors clearly or succeeds: `Use 1, 2 or 3.`

**`overview.md`, `<!-- TRAPS -->`:** one bullet for each trap the findings confirmed, drawn from this list (delete the marker if none):

```
- `update_user_book` and `update_user_book_read` clear fields you leave
  out; see the library and reading guides.
- `edition_owned` toggles; check `owned` first.
- `progress_seconds` needs an audiobook edition on the read.
- A journal entry needs `privacy_setting_id` 1, 2 or 3.
- `user_books` without a `user_id` filter returns other people's public
  entries too.
```

- Name only the mutation(s) L1 and L1-read actually found clearing fields.
- Include the last bullet only if L3 found other users' rows.

- [ ] **Step 7: Run the tests**

Run: `go test -count=1 ./internal/reference/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/reference`. If an example you added fails to validate, fix its GraphQL, not the test.

- [ ] **Step 8: Commit**

```bash
git add internal/reference
git commit -m "Add the guides hardcover_docs serves, validated against the schema"
```

---

### Task 10: MCP tools

**Files:**
- Create: `internal/mcpserver/mcpserver.go`
- Test: `internal/mcpserver/mcpserver_test.go`

**Interfaces:**
- Consumes: `oauth.Server.Authenticate`, `oauth.ErrUnauthorized`, `oauth.Caller`, `Challenge`, `MintAccessToken`, `oauth.User`, `oauth.Grant` (Task 6); `hardcover.Client.Do`, `hardcover.Error`, `hardcover.KeyURL`, `hardcover.CodeTooLarge` (Task 4); `reference.Guide`, `Lookup`, `Overview`, `Topics` (Tasks 8–9); `reqlog.From` and `Fields` (Task 7).
- Produces:
  - `mcpserver.Handler{OAuth *oauth.Server; Hardcover *hardcover.Client; Version string; Log *slog.Logger}`, an `http.Handler`
  - `mcpserver.GraphQLInput`, `mcpserver.DocsInput`

- [ ] **Step 1: Add the dependency and write the failing test**

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.8.0
```

```go
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
	logs  *[][]any
}

// newEnv serves the handler for a connected user whose Hardcover key is
// "key-adam", recording each request's reqlog fields as the access log
// would.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	sl := sealer.NewLocal()
	oa := &oauth.Server{Store: st, Sealer: sl, Issuer: "https://dustjacket.test"}
	sealed, err := sl.Seal(ctx, "7", []byte("key-adam"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, oauth.User{ID: "7", Username: "adam", KeyCiphertext: sealed}); err != nil {
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
	return &env{url: srv.URL, token: tok, hc: hc, logs: logs}
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
	// Titles and reviews are not ASCII; they pass through byte for byte.
	e.hc.set(200, map[string]string{"RateLimit": `"Free";r=58;t=60`}, `{"data":{"me":[{"id":7,"username":"adam","bio":"Brontë ✓ 読書"}]}}`)
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
	if out.RateLimit != `"Free";r=58;t=60` || string(out.Response) != `{"data":{"me":[{"id":7,"username":"adam","bio":"Brontë ✓ 読書"}]}}` {
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
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/mcpserver/`
Expected: FAIL to build, with `undefined: Handler`.

- [ ] **Step 3: Implement**

```go
// Package mcpserver is /mcp: a Streamable HTTP MCP server authenticated by
// Dustjacket's own OAuth tokens, whose three tools run Hardcover's GraphQL
// API as the person the token was granted for. Stateless with JSON
// responses, so it works behind a buffered Lambda function URL.
package mcpserver

import (
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
	// Tools without listChanged: the list is fixed for the life of the
	// binary, and a client that subscribes to changes anyway would
	// otherwise hold its request open until Lambda times it out.
	s := mcp.NewServer(&mcp.Implementation{Name: "dustjacket", Title: "Dustjacket", Version: h.Version}, &mcp.ServerOptions{
		Instructions: instructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	t := &tools{hc: h.Hardcover, key: c.Key, log: fields}
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
	hc  *hardcover.Client
	key string
	log *reqlog.Fields
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
		return nil, nil, errors.New(describe(err))
	}
	text, err := json.Marshal(struct {
		RateLimit string          `json:"rate_limit"`
		Response  json.RawMessage `json:"response"`
	}{res.RateLimit, res.Body})
	if err != nil {
		return nil, nil, err
	}
	if len(text) > maxResult {
		return nil, nil, errors.New(tooLarge(len(text)))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}, IsError: res.HasErrors}, nil, nil
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

func tooLarge(n int) string {
	return fmt.Sprintf("The response was %d KB, over the 100 KB limit. Ask for fewer fields or add a `limit`.", n>>10)
}

// describe turns a failed Hardcover request into what Claude should know
// to act on it.
func describe(err error) string {
	var he *hardcover.Error
	if !errors.As(err, &he) {
		return "The request to Hardcover failed: " + err.Error()
	}
	switch {
	case he.Code == hardcover.CodeTooLarge:
		return tooLarge(4 << 20)
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
```

- [ ] **Step 4: Run the tests**

Run: `go mod tidy && go test -count=1 ./internal/mcpserver/ && go vet ./internal/mcpserver/`
Expected: `ok  github.com/adamrothman/dustjacket/internal/mcpserver`. If `go test` reports missing `go.sum` entries, that means `go mod tidy` didn't run: run it, then retry.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/mcpserver
git commit -m "Add the MCP endpoint: hardcover_query, hardcover_mutate, hardcover_docs"
```

---

### Task 11: The binary, end-to-end test, CI

**Files:**
- Create: `cmd/dustjacket/config.go`, `cmd/dustjacket/main.go`
- Test: `cmd/dustjacket/config_test.go`, `cmd/dustjacket/main_test.go`
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: every package above.
- Produces:
  - The `dustjacket` binary: the Lambda entry point, plus `dustjacket serve [-addr :8080] [-memory]`
  - `build(ctx, memory bool) (http.Handler, config, error)`
  - `loadConfig() config`

- [ ] **Step 1: Add the dependencies and write the failing tests**

```bash
go get github.com/aws/aws-lambda-go@v1.55.0 github.com/awslabs/aws-lambda-go-api-proxy@v0.16.2 github.com/aws/aws-sdk-go-v2/config@v1.33.5
```

`cmd/dustjacket/config_test.go`:

```go
package main

import (
	"slices"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("DUSTJACKET_BASE_URL", "https://dustjacket.rothman.tools/")
	t.Setenv("DUSTJACKET_ALLOWED_USERS", " adam, Sister ,,")
	t.Setenv("DUSTJACKET_OAUTH_REDIRECT_HOSTS", "")
	c := loadConfig()
	if c.BaseURL != "https://dustjacket.rothman.tools" {
		t.Errorf("base URL %q", c.BaseURL)
	}
	if !slices.Equal(c.AllowedUsers, []string{"adam", "Sister"}) {
		t.Errorf("allowed %q", c.AllowedUsers)
	}
	if !slices.Equal(c.RedirectHosts, []string{"claude.ai", "claude.com"}) || c.Table != "dustjacket" || c.HardcoverURL != "https://api.hardcover.app/v1/graphql" {
		t.Errorf("defaults: %+v", c)
	}
}
```

`cmd/dustjacket/main_test.go`:

```go
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
	t.Setenv("DUSTJACKET_ALLOWED_USERS", "adam")
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
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 3 {
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
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/dustjacket/`
Expected: FAIL to build, with `undefined: loadConfig` and `undefined: build`.

- [ ] **Step 3: Implement**

`cmd/dustjacket/config.go`:

```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/mcpserver"
	"github.com/adamrothman/dustjacket/internal/oauth"
	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
	"github.com/adamrothman/dustjacket/internal/web"
)

// config is the DUSTJACKET_* environment.
type config struct {
	BaseURL       string
	Table         string
	KMSKeyID      string
	AllowedUsers  []string
	OriginVerify  string
	RedirectHosts []string
	HardcoverURL  string
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv("DUSTJACKET_" + name)); v != "" {
		return v
	}
	return def
}

func list(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func loadConfig() config {
	return config{
		BaseURL:       strings.TrimSuffix(env("BASE_URL", "http://localhost:8080"), "/"),
		Table:         env("TABLE", "dustjacket"),
		KMSKeyID:      env("KMS_KEY_ID", ""),
		AllowedUsers:  list(env("ALLOWED_USERS", "")),
		OriginVerify:  env("ORIGIN_VERIFY", ""),
		RedirectHosts: list(env("OAUTH_REDIRECT_HOSTS", "claude.ai,claude.com")),
		HardcoverURL:  env("HARDCOVER_URL", hardcover.DefaultURL),
	}
}

// version is the commit the binary was built from, or "dev".
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

// build wires everything. With memory it needs no AWS: an in-memory store
// and a sealer whose key dies with the process.
func build(ctx context.Context, memory bool) (http.Handler, config, error) {
	cfg := loadConfig()
	var st store.Store
	var sl sealer.Sealer
	if memory {
		st, sl = store.NewMemory(), sealer.NewLocal()
	} else {
		if cfg.KMSKeyID == "" {
			return nil, cfg, errors.New("DUSTJACKET_KMS_KEY_ID is required")
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, cfg, err
		}
		st = store.NewDynamo(dynamodb.NewFromConfig(awsCfg), cfg.Table)
		sl = &sealer.KMS{Client: kms.NewFromConfig(awsCfg), KeyID: cfg.KMSKeyID}
	}
	if len(cfg.AllowedUsers) == 0 {
		slog.Warn("DUSTJACKET_ALLOWED_USERS is empty: nobody can connect")
	}
	oa := &oauth.Server{Store: st, Sealer: sl, Issuer: cfg.BaseURL, AllowedRedirectHosts: cfg.RedirectHosts}
	hc := &hardcover.Client{URL: cfg.HardcoverURL, UserAgent: "dustjacket/" + version() + " (+" + cfg.BaseURL + ")"}
	srv := &web.Server{
		OAuth:        oa,
		Hardcover:    hc,
		MCP:          &mcpserver.Handler{OAuth: oa, Hardcover: hc, Version: version()},
		BaseURL:      cfg.BaseURL,
		Allowed:      cfg.AllowedUsers,
		OriginVerify: cfg.OriginVerify,
	}
	return srv.Handler(), cfg, nil
}
```

`cmd/dustjacket/main.go`:

```go
// dustjacket is the whole server in one binary: on Lambda it serves the
// function URL; locally it serves HTTP.
//
//	dustjacket                          # Lambda entry point (AWS_LAMBDA_RUNTIME_API set)
//	dustjacket serve [-addr :8080] [-memory]
//
// Configuration is DUSTJACKET_* environment variables (config.go).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx := context.Background()
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" && len(os.Args) == 1 {
		runLambda(ctx)
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: dustjacket serve [-addr :8080] [-memory]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	memory := fs.Bool("memory", false, "in-memory store and local sealer; no AWS")
	_ = fs.Parse(os.Args[2:])
	handler, cfg, err := build(ctx, *memory)
	if err != nil {
		fatal(err)
	}
	slog.Info("listening", "addr", *addr, "base_url", cfg.BaseURL, "memory", *memory)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	slog.Error("fatal", "err", err)
	os.Exit(1)
}

// budgetMargin is how long before Lambda's deadline a request's own work
// stops: long enough to log and answer, which a Lambda timeout would cut
// off.
const budgetMargin = 3 * time.Second

// budget is the context a request works under: Lambda's deadline less
// budgetMargin, so every call that honors its context (Hardcover, the AWS
// SDK) gives up first.
func budget(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline.Add(-budgetMargin))
}

func runLambda(ctx context.Context) {
	handler, _, err := build(ctx, false)
	if err != nil {
		fatal(err)
	}
	adapter := httpadapter.NewV2(handler)
	lambda.Start(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		ctx, stop := budget(ctx)
		defer stop()
		return adapter.ProxyWithContext(ctx, req)
	})
}
```

- [ ] **Step 4: Run everything CI runs**

```bash
go mod tidy
test -z "$(gofmt -l .)" && go vet ./... && go test -count=1 ./... && go test -race -count=1 ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/dustjacket && echo lambda-build-ok
```

Expected: `ok` for all nine packages in both runs, then `lambda-build-ok`.

- [ ] **Step 5: Look at the pages locally**

```bash
DUSTJACKET_ALLOWED_USERS=adam go run ./cmd/dustjacket serve -memory
```

1. Open http://localhost:8080 and check the home page.
2. Register a client with `curl -s -X POST localhost:8080/oauth/register -d '{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}'`.
3. Open `http://localhost:8080/oauth/authorize?response_type=code&client_id=<id>&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback&code_challenge=0123456789012345678901234567890123456789012&code_challenge_method=S256` and check the connect page. The tests only check its markup, so look at it at desktop and phone widths, in light and dark mode.
4. Stop the server.

- [ ] **Step 6: Add CI**

`.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - name: Format
        run: test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
      - name: Vet
        run: go vet ./...
      - name: Test
        run: go test ./...
      - name: Build for Lambda
        env:
          GOOS: linux
          GOARCH: arm64
          CGO_ENABLED: "0"
        run: go build -o /dev/null ./cmd/dustjacket
```

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum cmd .github/workflows/ci.yml
git commit -m "Add the dustjacket binary, the end-to-end test, and CI"
```

---

### Task 12: Docs and the deploy workflow

**Files:**
- Create: `CLAUDE.md`, `README.md` (replacing the one-line README), `docs/decisions.md`, `docs/runbook.md`
- Create: `.github/workflows/deploy.yml`

- [ ] **Step 1: Write the docs**

`CLAUDE.md`:

~~~markdown
# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Dustjacket, a remote MCP server that lets claude.ai and the Claude apps use a person's [Hardcover](https://hardcover.app) account: one Go binary on Lambda behind CloudFront at https://dustjacket.rothman.tools. Each person connects with their own Hardcover API key, which is sealed with KMS and stored in DynamoDB; an allowlist of Hardcover usernames decides who may connect (Adam and his sister). `README.md` has the layout and how to run it; `docs/superpowers/specs/2026-09-23-dustjacket-design.md` is the original design; `docs/decisions.md` is the numbered record of every design choice since, and is the authority where the two differ; `docs/runbook.md` is how Adam operates it; `docs/hardcover-notes.md` is what the live checks found about Hardcover's API. Infrastructure is Terraform in the `personal-infra` repo (`terraform/aws/acct-apps/dustjacket`); deploys happen from this repo's `main` via `.github/workflows/deploy.yml`.

## Commands

```sh
go test ./...                                   # everything; in-memory store, local sealer, fake Hardcover
gofmt -l . && go vet ./...                      # CI fails on either
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/dustjacket   # the Lambda build CI checks
go run ./cmd/dustjacket serve -memory           # local server, no AWS
./scripts/update-schema.sh                      # refresh the schema snapshot, then go test ./internal/reference/
HARDCOVER_TOKEN=... HARDCOVER_TEST_BOOK_ID=... go test -tags live -v -count=1 ./internal/hardcover/   # live checks, by hand only
```

Configuration is `DUSTJACKET_*` environment variables (`cmd/dustjacket/config.go`). There are no secrets in SSM: OAuth tokens are random and stored hashed, Hardcover keys are KMS-sealed, and the origin-verify value comes from Terraform.

## How it fits together

- **Tools** (`internal/mcpserver`): `hardcover_query` (read-only; the server parses the document and refuses anything but queries), `hardcover_mutate` (mutations only), `hardcover_docs` (the overview, a topic guide, or a schema lookup). The GraphQL tools pass Hardcover's JSON back verbatim with its `RateLimit` header, refuse results over 100 KB, and turn Hardcover's refusals into messages Claude can act on. Stateless Streamable HTTP with JSON responses and no `listChanged`, since a buffered function URL cannot hold a stream open.
- **Reference** (`internal/reference`): the guides Claude reads (`guides/*.md`, written from the live checks) and lookups in `schema.graphql`, a snapshot from Hardcover's docs repository. Every GraphQL example in a guide is validated against the snapshot by the tests.
- **Hardcover** (`internal/hardcover`): a small GraphQL client (20-second timeout, 4 MB read cap), `Me`, and `Scopes`/`KeyURL`, the key scopes the connect page pre-selects.
- **Auth** (`internal/oauth`): an OAuth 2.1 server with PKCE, dynamic client registration restricted to claude.ai and claude.com, hashed opaque tokens (1-hour access, rotating 30-day refresh), and clients that expire after a day unless someone connects through them. There is no login: the connect page (`internal/web`) takes a Hardcover key, asks Hardcover's `me` whose it is, checks the allowlist, and `Connect` records the user (key sealed), the grant and the code in one transaction. Keys live on the user record, so reconnecting updates every connection.
- **Store** (`internal/store`): one DynamoDB table (`USER#`, `OAUTHCLIENT#`, `GRANT#`, `AUTHCODE#`, `TOKEN#`, each with SK `META`), TTL on `ttl`; `store.Memory` is the in-memory twin the tests use.
- **Sealer** (`internal/sealer`): KMS `Encrypt`/`Decrypt` with the Hardcover user ID as encryption context; `Local` (AES-GCM, user ID as AAD) for tests and `serve -memory`.
- **Logging**: one `request` line per request (`internal/web`); the MCP tools add their fields through `internal/reqlog`. Never query text, variables, keys, bodies or query strings.
- **Timeouts**: every Lambda invocation works under a budget ending 3 s before the function's 29 s timeout, which sits below CloudFront's 30 s.

## Conventions

- This file must stay true. A change that contradicts anything here updates it in the same commit, as it does `README.md`, `docs/decisions.md` and `docs/runbook.md` where the change touches what they describe.
- A change to how Dustjacket works gets a new numbered entry in `docs/decisions.md` (or amends the entry it revises, saying so).
- Never state something about Hardcover's API that isn't in its docs, its schema, or `docs/hardcover-notes.md`. When a guide needs a fact nobody has checked, add a live check and run it.
- Tests drive the real handlers: the OAuth flow over `httptest` with the form a browser posts, the MCP tools through a real go-sdk client, Hardcover through a fake server. Test the shape the client actually sends.
- Secrets and personal data never enter the repo or the logs.
~~~

`README.md`:

~~~markdown
# dustjacket

A remote MCP server that connects Claude to [Hardcover](https://hardcover.app),
at [dustjacket.rothman.tools](https://dustjacket.rothman.tools). Each
person connects with their own Hardcover API key; Claude then gets three
tools: `hardcover_query` and `hardcover_mutate`, which run Hardcover's
GraphQL API as that person, and `hardcover_docs`, guides and schema
lookups that tell Claude how. One Go binary on AWS Lambda, DynamoDB, KMS.

`docs/superpowers/specs/2026-09-23-dustjacket-design.md` is the design,
`docs/decisions.md` the record of decisions since, `docs/runbook.md` how
it is operated, and `docs/hardcover-notes.md` what the live checks found
about Hardcover's API.

## Layout

| Path | What |
| --- | --- |
| `cmd/dustjacket` | The binary: Lambda entry point and `serve`; configuration |
| `internal/mcpserver` | `/mcp` and the three tools |
| `internal/reference` | Guides for Claude, the schema snapshot, lookups |
| `internal/hardcover` | GraphQL client, `Me`, the key scopes; live checks (`-tags live`) |
| `internal/oauth` | OAuth 2.1 authorization server, users, grants |
| `internal/web` | Routes, the connect page, middleware |
| `internal/sealer` | KMS and local sealing of Hardcover keys |
| `internal/store` | DynamoDB single table and its in-memory twin |
| `internal/reqlog` | Log fields from the tools to the request's log line |
| `scripts/update-schema.sh` | Refreshes the schema snapshot |

## Run it locally

```sh
go test ./...
DUSTJACKET_ALLOWED_USERS=<hardcover username> go run ./cmd/dustjacket serve -memory
```

## Deploy

Pushes to `main` run `.github/workflows/deploy.yml`: test, build the arm64
binary, `aws lambda update-function-code` via the `github-actions-dustjacket`
role. Terraform (`personal-infra`, `acct-apps/dustjacket`) owns the
function's configuration and ignores its code.
~~~

`docs/decisions.md`: if Task 4 changed how refusals are read (L9), or dropped `write:reviews` (L5), add a numbered entry saying what the live checks found and what changed.

~~~markdown
# Decisions

The numbered record of every design choice since the design spec
(`docs/superpowers/specs/2026-09-23-dustjacket-design.md`). Where the two
differ, this file wins. A change to how Dustjacket works gets a new entry,
or amends the one it revises and says so.

1. **Remote, on Lambda.** One Go binary behind CloudFront, following
   pickem, so claude.ai and the Claude apps can reach it from anywhere.
2. **Three tools: GraphQL query, GraphQL mutation, docs.** Hardcover's API
   is a Hasura GraphQL API with about 1,300 types and 260 root fields.
   Curated tools (the prior art has 39) cost code, maintenance against a
   beta API, and context, and still don't cover open-ended questions.
   Reads and writes are separate tools so claude.ai's per-tool permissions
   can let reads run freely while writes ask first. A curated tool is
   added only if real use shows Claude repeatedly getting a particular
   write wrong.
3. **The Hardcover key is the identity.** No other login: the connect page
   asks for a key, and Hardcover's `me` says whose it is.
4. **Allowlist by Hardcover username**, in `DUSTJACKET_ALLOWED_USERS`, set
   by Terraform (`locals.allowed_users`). A renamed account is locked out
   until the list is updated, which fails safe.
5. **Keys are sealed with a customer-managed KMS key**, by direct `Encrypt`
   and `Decrypt` with the Hardcover user ID as encryption context. An AES
   key in SSM would save $1 a month but would let anyone holding the
   parameter and the table decrypt everything offline, unaudited.
6. **Keys live on the user, not the grant.** Reconnecting with a new key
   updates the user record, so every connection that person has picks it
   up.
7. **Unused OAuth clients expire.** Dynamic client registration is open, as
   MCP expects; a client nobody connects through within 24 hours is
   deleted by DynamoDB TTL, and treated as unknown from then even before
   the deletion. The first successful connect makes it permanent.
8. **No change notifications.** Tools are advertised without `listChanged`
   and there is no logging capability, for the reason in pickem's decision
   30: a buffered function URL cannot hold a stream open.
9. **No retries on Hardcover's 429.** The tool reports the wait and Claude
   decides; retrying inside a 29-second Lambda budget would mostly time
   out.
10. **Responses over 100 KB are refused** with a request for a narrower
    query, to protect the conversation's context. The threshold is a
    starting point.
11. **The key's scopes are pre-selected** on Hardcover's key page by the
    connect page's link (`hardcover.Scopes`): the smallest set covering
    logging reading, library questions, discovery, lists, goals, journal
    and recommendations. Excluded: email, roles, account data,
    notifications, prompts, vibes, following/liking/blocking, and
    librarian catalog edits.
12. **Hosted at `dustjacket.rothman.tools`.** `rothman.tools` is Adam's
    domain for AI connectors, one subdomain each; its zone lives in
    `acct-apps` beside the functions it serves. The subdomain is the
    project's name, not Hardcover's, so it does not read as an official
    Hardcover service.
13. **The schema snapshot comes from Hardcover's docs repository**
    (`hardcoverapp/hardcover-docs`, MIT), refreshed by
    `scripts/update-schema.sh`, rather than live introspection: lookups
    are free, fast and testable, and the guides are validated against it.
14. **No point-in-time recovery.** Losing the table means everyone
    reconnects; nothing else is lost.
15. **Logs never carry query text, variables, keys, request bodies or query
    strings.** Mutations can carry reviews and private notes; query
    strings carry OAuth codes.
16. **The connect page sends `Referrer-Policy: same-origin` and a CSP
    without `form-action`.** Under `no-referrer`, browsers send
    `Origin: null` with the form, which the origin check rejects;
    `same-origin` keeps the Origin and still sends Hardcover's key page
    nothing. `form-action` would also govern the redirect to the client's
    callback, hop by hop, and the page has nothing a form could be
    injected into.
~~~

`docs/runbook.md`:

~~~markdown
# Runbook

How Adam operates Dustjacket. Infrastructure is the
`terraform/aws/acct-apps/dustjacket` stack in `personal-infra`; every AWS
CLI command here needs `--profile apps` (or `AWS_PROFILE=apps`).

## Connecting claude.ai

1. In claude.ai, add a custom connector with the URL
   `https://dustjacket.rothman.tools/mcp`.
2. claude.ai opens Dustjacket's connect page. Follow **Create a Hardcover
   API key** (the scopes are pre-selected), copy the key, paste it, and
   press **Connect**.
3. Claude now has `hardcover_query`, `hardcover_mutate` and
   `hardcover_docs`. Keep approving `hardcover_mutate` call by call rather
   than always allowing it: Hardcover returns other people's reviews and
   list descriptions, which are text Claude reads while a write tool is
   available.

## Adding someone

1. Ask for their Hardcover username.
2. Add it to `locals.allowed_users` in the stack's `locals.tf`, and apply.
   The connect page reads the list from the function's environment, so the
   change is live as soon as the apply finishes.
3. Send them the steps under "Connecting claude.ai".

Someone not on the list who tries to connect sees "@username isn't on
this server's allowlist".

## A key expired or was revoked

Every tool call then says "Hardcover rejected your API key". The person
disconnects and reconnects Dustjacket in Claude's settings, and pastes a
new key. That updates their stored key for all of their connections.

## Revoking access

Any of these, from quickest to most thorough:

- The person deletes the key at hardcover.app/account/api. Nothing
  Dustjacket holds works after that.
- The person removes the connector in Claude.
- Adam deletes their user record, which breaks all of their connections
  at once:

  ```sh
  aws --profile apps dynamodb delete-item --table-name dustjacket \
    --key '{"PK":{"S":"USER#<hardcover user id>"},"SK":{"S":"META"}}'
  ```

  The user ID is in the `connected` log line (`username=...`), or ask
  Hardcover: `query { me { id } }` with their key.

## Refreshing the schema snapshot

```sh
./scripts/update-schema.sh
go test ./internal/reference/
```

If the tests fail, a guide's example no longer matches the schema: fix the
guide, then commit the snapshot and the guide together.

## Re-running the live checks

When Hardcover changes something, or before relying on a trap the guides
describe, rerun the checks in `docs/hardcover-notes.md` with a key saved at
`~/.config/dustjacket/hardcover-token` and a book that is not in the
library:

```sh
HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) \
HARDCOVER_TEST_BOOK_ID=<book id> \
go test -tags live -v -count=1 ./internal/hardcover/
```

They add the test book to the library and remove it again. Update the
notes and the guides with anything that changed.

## When the error alarm fires

`dustjacket-lambda-errors` emails when an invocation fails outright (a
crash, or start-up failing, for example a missing
`DUSTJACKET_KMS_KEY_ID`). Everything is in the `/aws/lambda/dustjacket`
log group:

- Every request logs one `request` line when it finishes: method, path,
  status, `ms`; on `/mcp` also the user, the tool, the operation type, the
  top-level fields, and Hardcover's status (`hc_status`, 0 for a timeout)
  and latency (`hc_ms`). Never the query or its variables.
- `connect: key rejected` and `connect: not on the allowlist` are the
  connect page turning someone away; `connected` is a success.
- `authenticate` at error level is a store or KMS failure while checking a
  token: look at the error for which.
- A `REPORT` line marked `Status: timeout` means something ignored its
  context and Lambda killed it at 29 seconds.

## Local development

```sh
go test ./...
DUSTJACKET_ALLOWED_USERS=<your username> go run ./cmd/dustjacket serve -memory
```

`-memory` needs no AWS. claude.ai cannot reach localhost, so the OAuth
flow cannot be finished by hand locally; `cmd/dustjacket`'s end-to-end
test walks the whole flow instead. The local server is for looking at the
pages.
~~~

- [ ] **Step 2: Check the docs against the code**

Read `CLAUDE.md` and `README.md` against the tree:
- Every path they name exists.
- Every command they give runs. At least run the `go test`, `gofmt` and build lines.
- Fix any mismatch in the docs.

- [ ] **Step 3: Add the deploy workflow**

`.github/workflows/deploy.yml`. It does nothing useful until Task 14 creates the role and the function.

```yaml
# Pushes to main build the arm64 Lambda binary and update the function's
# code. Terraform (personal-infra, acct-apps/dustjacket) owns the
# function's configuration and ignores code changes; this workflow owns
# the code.
name: Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

concurrency: deploy

permissions:
  contents: read
  id-token: write

env:
  AWS_REGION: us-west-2
  FUNCTION_NAME: dustjacket

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - name: Test
        run: go test ./...
      - name: Build
        env:
          GOOS: linux
          GOARCH: arm64
          CGO_ENABLED: "0"
        run: |
          go build -trimpath -ldflags="-s -w" -o bootstrap ./cmd/dustjacket
          zip -q dustjacket.zip bootstrap
      - uses: aws-actions/configure-aws-credentials@v5
        with:
          role-to-assume: arn:aws:iam::<apps account ID>:role/github-actions-dustjacket
          aws-region: ${{ env.AWS_REGION }}
      - name: Update function code
        run: |
          aws lambda update-function-code --function-name "$FUNCTION_NAME" --zip-file fileb://dustjacket.zip > /dev/null
          aws lambda wait function-updated --function-name "$FUNCTION_NAME"
          aws lambda get-function-configuration --function-name "$FUNCTION_NAME" --query '{Version:Version,LastModified:LastModified,CodeSize:CodeSize}'
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md README.md docs/decisions.md docs/runbook.md .github/workflows/deploy.yml
git commit -m "Add CLAUDE.md, README, decisions, runbook and the deploy workflow"
```

---

### Task 13: Terraform stack in personal-infra

**Files (in `~/src/personal-infra`):**
- Create: `terraform/aws/acct-apps/dustjacket/{terraform,providers,data,locals,kms,dynamodb,lambda,cloudfront,acm,route53,alerting,github-actions,outputs}.tf`, `CLAUDE.md`
- Modify: `CLAUDE.md` (the layout list and the apply-order paragraph)

**Interfaces:**
- Consumes:
  - The `rothman.tools` zone in `acct-apps/dns`. Adam added it on branch `apps-rothman-tools-zone`, commit `81fc669`.
  - The GitHub Actions OIDC provider from `acct-apps/global`.
- Produces:
  - The Lambda function `dustjacket`, with the environment Task 11's `loadConfig` reads
  - The role `github-actions-dustjacket`, which Task 12's deploy workflow assumes
  - The alias `alias/dustjacket`
  - The table `dustjacket`

- [ ] **Step 1: Branch from a main that has the zone**

```bash
cd ~/src/personal-infra
git fetch origin 2>/dev/null; git checkout main && git pull --ff-only
git log --oneline -20 | grep -i 'rothman.tools'
```

- **Expected:** the zone commit is on `main`. Then run `git checkout -b apps-dustjacket`.
- **If it isn't:** ask Adam whether to wait for his zone branch to merge, or to branch from `apps-rothman-tools-zone`.
- **Also ask Adam:** whether the zone is applied, and whether the registrar's nameservers point at it. Check with `dig +short NS rothman.tools`: it should print the Route 53 name servers from `terraform output rothman_tools` in `acct-apps/dns`.

- [ ] **Step 2: Write the stack**

In `terraform/aws/acct-apps/dustjacket/`:

`terraform.tf`:

```hcl
terraform {
  backend "s3" {
    bucket       = "adamrothman-tfstate"
    encrypt      = true
    key          = "aws/<apps account ID>/dustjacket.tfstate"
    profile      = "tfstate"
    region       = "us-west-2"
    use_lockfile = true
  }

  required_providers {
    # The placeholder Lambda deployment package (lambda.tf)
    archive = {
      source  = "hashicorp/archive"
      version = ">= 2.0.0, < 3.0.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0.0, < 7.0.0"
    }
    # The CloudFront origin-verify secret (lambda.tf)
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0.0, < 4.0.0"
    }
  }

  # The S3 backend's use_lockfile requires 1.10
  required_version = ">= 1.10"
}
```

`providers.tf`:

```hcl
provider "aws" {
  allowed_account_ids = ["<apps account ID>"]
  profile             = "apps"
  region              = "us-west-2"

  default_tags {
    tags = {
      ManagedBy = "terraform"
      Stack     = "dustjacket"
    }
  }
}

# CloudFront-attached ACM certificates can only be requested in us-east-1
# (acm.tf).
provider "aws" {
  alias               = "use1"
  allowed_account_ids = ["<apps account ID>"]
  profile             = "apps"
  region              = "us-east-1"

  default_tags {
    tags = {
      ManagedBy = "terraform"
      Stack     = "dustjacket"
    }
  }
}
```

`data.tf`:

```hcl
data "aws_caller_identity" "current" {}

data "aws_region" "current" {}
```

`locals.tf`:

```hcl
# The app's knobs. Everything the Lambda needs to know about its
# surroundings is passed as DUSTJACKET_* environment variables
# (lambda.tf) and derives from these.
locals {
  alert_email = "rothman.adam+dustjacket@gmail.com"
  # Hardcover usernames that may connect (case-insensitive). Adding
  # someone is an edit here and an apply; the app reads the list from
  # DUSTJACKET_ALLOWED_USERS.
  allowed_users = ["ADAM_HARDCOVER_USERNAME", "SISTER_HARDCOVER_USERNAME"]
  base_url      = "https://${local.domain}"
  domain        = "${local.name}.${local.zone_domain}"
  name          = "dustjacket"
  zone_domain   = "rothman.tools"
}
```

`kms.tf`:

```hcl
# Seals each person's Hardcover API key before it is stored: the app
# calls Encrypt and Decrypt directly, with the Hardcover user ID as the
# encryption context, so a ciphertext copied onto another person's row
# does not decrypt (dustjacket repo, internal/sealer). The key policy is
# the default one, which defers to IAM; the Lambda role's grant is in
# lambda.tf, and every Decrypt is in CloudTrail.
#
# Destroying the key would leave every stored key unreadable (everyone
# would have to reconnect), hence prevent_destroy.
resource "aws_kms_key" "keys" {
  deletion_window_in_days = 30
  description             = "dustjacket: seals Hardcover API keys"
  enable_key_rotation     = true

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_kms_alias" "keys" {
  name          = "alias/${local.name}"
  target_key_id = aws_kms_key.keys.key_id
}
```

`dynamodb.tf`:

```hcl
# Single-table design: users, OAuth clients, grants, authorization codes
# and tokens share one table, keyed by PK/SK. The key scheme is
# documented in the dustjacket repo (its design spec); Terraform only
# knows the attribute names. TTL on `ttl` expires codes, tokens and
# OAuth clients nobody connected through. No point-in-time recovery:
# losing the table only means everyone reconnects.
resource "aws_dynamodb_table" "app" {
  billing_mode                = "PAY_PER_REQUEST"
  deletion_protection_enabled = true
  hash_key                    = "PK"
  name                        = local.name
  range_key                   = "SK"

  attribute {
    name = "PK"
    type = "S"
  }

  attribute {
    name = "SK"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  lifecycle {
    prevent_destroy = true
  }
}
```

`lambda.tf`:

```hcl
# One Go binary serves everything through the function URL (behind
# CloudFront, cloudfront.tf). Terraform owns the function's
# configuration; the dustjacket repo's GitHub Actions deploy workflow
# owns its code (`aws lambda update-function-code`, via the role in
# github-actions.tf).

resource "aws_cloudwatch_log_group" "app" {
  name              = "/aws/lambda/${local.name}"
  retention_in_days = 90
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      identifiers = ["lambda.amazonaws.com"]
      type        = "Service"
    }
  }
}

resource "aws_iam_role" "lambda" {
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
  name               = "${local.name}-lambda"
}

# Scoped to the function's own log group rather than the
# AWSLambdaBasicExecutionRole managed policy, which grants logs:* on
# every group in the account.
data "aws_iam_policy_document" "lambda_logs" {
  statement {
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.app.arn}:*"]
  }
}

resource "aws_iam_role_policy" "lambda_logs" {
  name   = "logs"
  policy = data.aws_iam_policy_document.lambda_logs.json
  role   = aws_iam_role.lambda.id
}

data "aws_iam_policy_document" "lambda_dynamodb" {
  statement {
    actions = [
      "dynamodb:ConditionCheckItem",
      "dynamodb:DeleteItem",
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:TransactWriteItems",
    ]
    resources = [aws_dynamodb_table.app.arn]
  }
}

resource "aws_iam_role_policy" "lambda_dynamodb" {
  name   = "dynamodb"
  policy = data.aws_iam_policy_document.lambda_dynamodb.json
  role   = aws_iam_role.lambda.id
}

# The one key that seals Hardcover API keys (kms.tf), and nothing else.
data "aws_iam_policy_document" "lambda_kms" {
  statement {
    actions = [
      "kms:Decrypt",
      "kms:Encrypt",
    ]
    resources = [aws_kms_key.keys.arn]
  }
}

resource "aws_iam_role_policy" "lambda_kms" {
  name   = "kms"
  policy = data.aws_iam_policy_document.lambda_kms.json
  role   = aws_iam_role.lambda.id
}

# Shared secret between CloudFront and the function: CloudFront sends it
# as the X-Origin-Verify origin header (cloudfront.tf) and the app
# rejects requests without it, so the raw function URL (which must be
# public, see below) cannot be used to bypass CloudFront. Changing it
# here rolls both sides together.
resource "random_password" "origin_verify" {
  length  = 32
  special = false
}

# Placeholder deployment package so the function can be created before
# the app has ever been deployed: a bootstrap that exits non-zero, so an
# invocation before the first real deploy fails loudly (and trips the
# errors alarm) instead of pretending to work. The deploy workflow
# replaces it on the first push to main.
data "archive_file" "bootstrap" {
  output_path = "${path.module}/.terraform/bootstrap.zip"
  type        = "zip"

  source {
    content  = "#!/bin/sh\nexit 1\n"
    filename = "bootstrap"
  }
}

# The timeout is below CloudFront's 30 s origin response timeout
# (cloudfront.tf), so a hung request is stopped before CloudFront gives
# up and returns a 504, as in the pickem stack. The app stops its own
# work 3 s before this and gives Hardcover 20 s per request.
#
# The lifecycle block is the ownership split described at the top of
# this file: once the deploy workflow has uploaded real code, Terraform
# must not put the placeholder back.
resource "aws_lambda_function" "app" {
  architectures    = ["arm64"]
  filename         = data.archive_file.bootstrap.output_path
  function_name    = local.name
  handler          = "bootstrap"
  memory_size      = 512
  role             = aws_iam_role.lambda.arn
  runtime          = "provided.al2023"
  source_code_hash = data.archive_file.bootstrap.output_base64sha256
  timeout          = 29

  environment {
    variables = {
      DUSTJACKET_ALLOWED_USERS = join(",", local.allowed_users)
      DUSTJACKET_BASE_URL      = local.base_url
      DUSTJACKET_KMS_KEY_ID    = aws_kms_key.keys.arn
      DUSTJACKET_ORIGIN_VERIFY = random_password.origin_verify.result
      DUSTJACKET_TABLE         = aws_dynamodb_table.app.name
    }
  }

  lifecycle {
    ignore_changes = [filename, source_code_hash]
  }

  # Create the log group (with its retention) before Lambda's first
  # invocation would create it with none.
  depends_on = [aws_cloudwatch_log_group.app]
}

# authorization_type NONE, not AWS_IAM with a CloudFront origin access
# control: OAC on a Lambda origin signs the request with SigV4, which
# for requests with a body requires the viewer to have sent
# x-amz-content-sha256, and neither browsers nor MCP clients do. The
# X-Origin-Verify header (above) is the substitute.
resource "aws_lambda_function_url" "app" {
  authorization_type = "NONE"
  function_name      = aws_lambda_function.app.function_name
  invoke_mode        = "BUFFERED"

  # Creating a URL with authorization_type NONE makes the provider add
  # the two statements below itself, under these same statement IDs,
  # ignoring "already exists" (and never removing them). Creating them
  # first keeps them Terraform's, instead of racing the provider.
  depends_on = [
    aws_lambda_permission.invoke_function,
    aws_lambda_permission.invoke_function_url,
  ]
}

# The public grant. Function URLs created since October 2025 need both
# actions; lambda:InvokeFunction is limited to calls through the URL.
resource "aws_lambda_permission" "invoke_function_url" {
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.app.function_name
  function_url_auth_type = "NONE"
  principal              = "*"
  statement_id           = "FunctionURLAllowPublicAccess"
}

resource "aws_lambda_permission" "invoke_function" {
  action                   = "lambda:InvokeFunction"
  function_name            = aws_lambda_function.app.function_name
  invoked_via_function_url = true
  principal                = "*"
  statement_id             = "FunctionURLAllowInvokeAction"
}
```

`cloudfront.tf`:

```hcl
data "aws_cloudfront_cache_policy" "caching_disabled" {
  name = "Managed-CachingDisabled"
}

# Forwards every viewer header, cookie, and query string except Host,
# which must stay the function URL's own hostname for Lambda to route
# the request.
data "aws_cloudfront_origin_request_policy" "all_viewer_except_host" {
  name = "Managed-AllViewerExceptHostHeader"
}

locals {
  # The function URL is "https://<id>.lambda-url.<region>.on.aws/"; the
  # origin wants the bare host.
  lambda_origin_id     = "${local.name}-lambda"
  lambda_origin_domain = trimsuffix(replace(aws_lambda_function_url.app.function_url, "https://", ""), "/")
}

# CloudFront in front of the function URL for the custom domain and TLS
# on dustjacket.rothman.tools. Nothing is cached: the one stylesheet is
# tiny and the rest is OAuth and MCP.
resource "aws_cloudfront_distribution" "app" {
  aliases         = [local.domain]
  enabled         = true
  http_version    = "http2and3"
  is_ipv6_enabled = true

  default_cache_behavior {
    allowed_methods          = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cache_policy_id          = data.aws_cloudfront_cache_policy.caching_disabled.id
    cached_methods           = ["GET", "HEAD"]
    compress                 = true
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer_except_host.id
    target_origin_id         = local.lambda_origin_id
    viewer_protocol_policy   = "redirect-to-https"
  }

  origin {
    domain_name = local.lambda_origin_domain
    origin_id   = local.lambda_origin_id

    custom_header {
      name  = "X-Origin-Verify"
      value = random_password.origin_verify.result
    }

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
      # AWS's default, set explicitly because the function's timeout
      # (lambda.tf) is kept below it.
      origin_read_timeout = 30
    }
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.app.certificate_arn
    minimum_protocol_version = "TLSv1.2_2021"
    ssl_support_method       = "sni-only"
  }
}
```

`acm.tf`:

```hcl
# One name, no wildcard: each connector on rothman.tools is its own
# stack with its own certificate. The validation records are in
# route53.tf.
resource "aws_acm_certificate" "app" {
  domain_name       = local.domain
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  # Certificates for use with CloudFront must be requested in us-east-1
  provider = aws.use1
}

resource "aws_acm_certificate_validation" "app" {
  certificate_arn         = aws_acm_certificate.app.arn
  validation_record_fqdns = [for record in aws_route53_record.app_validation : record.fqdn]

  provider = aws.use1
}
```

`route53.tf`:

```hcl
# The zone is owned by acct-apps/dns; apply that stack (and delegate the
# domain at the registrar) before this one.
data "aws_route53_zone" "rothman_tools" {
  name = local.zone_domain
}

resource "aws_route53_record" "app" {
  name    = local.domain
  type    = "A"
  zone_id = data.aws_route53_zone.rothman_tools.zone_id

  alias {
    evaluate_target_health = false
    name                   = aws_cloudfront_distribution.app.domain_name
    zone_id                = aws_cloudfront_distribution.app.hosted_zone_id
  }
}

resource "aws_route53_record" "app_aaaa" {
  name    = local.domain
  type    = "AAAA"
  zone_id = data.aws_route53_zone.rothman_tools.zone_id

  alias {
    evaluate_target_health = false
    name                   = aws_cloudfront_distribution.app.domain_name
    zone_id                = aws_cloudfront_distribution.app.hosted_zone_id
  }
}

resource "aws_route53_record" "app_validation" {
  for_each = {
    for dvo in aws_acm_certificate.app.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }

  allow_overwrite = true
  name            = each.value.name
  records         = [each.value.record]
  ttl             = 300
  type            = each.value.type
  zone_id         = data.aws_route53_zone.rothman_tools.zone_id
}
```

`alerting.tf`:

```hcl
# One topic, one alarm. The email subscription sits in "pending
# confirmation" until Adam clicks the link SNS sends; Terraform cannot
# confirm it.

resource "aws_sns_topic" "alerts" {
  name = "${local.name}-alerts"
}

resource "aws_sns_topic_subscription" "alerts_email" {
  endpoint  = local.alert_email
  protocol  = "email"
  topic_arn = aws_sns_topic.alerts.arn
}

# Errors counts invocations that ended in a function error (a panic the
# app did not recover, a crash at start-up). Requests the app answered
# with a 4xx or 5xx are not Errors; the app logs those.
#
# treat_missing_data = "notBreaching" is load-bearing: quiet periods emit
# no data at all for this metric, and that must read as healthy.
resource "aws_cloudwatch_metric_alarm" "lambda_errors" {
  alarm_actions       = [aws_sns_topic.alerts.arn]
  alarm_description   = "The dustjacket Lambda reported at least one invocation error in the last 5 minutes. Check the /aws/lambda/dustjacket log group."
  alarm_name          = "${local.name}-lambda-errors"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = 1

  dimensions = {
    FunctionName = aws_lambda_function.app.function_name
  }

  treat_missing_data = "notBreaching"
}
```

`github-actions.tf`:

```hcl
# CD: pushes to dustjacket's main branch build the arm64 binary in
# GitHub Actions and upload it straight to the function (dustjacket
# .github/workflows/deploy.yml). The account-wide GitHub Actions OIDC
# provider is owned by the global stack.
data "aws_iam_openid_connect_provider" "github_actions" {
  url = "https://token.actions.githubusercontent.com"
}

# Only runs on dustjacket's main branch can assume the role. The
# @-qualified immutable user/repo IDs guard against name reuse and
# require use_immutable_subject=true in the repo's OIDC sub
# customization:
#   gh api -X PUT repos/adamrothman/dustjacket/actions/oidc/customization/sub \
#     -F use_default=true -F use_immutable_subject=true
data "aws_iam_policy_document" "github_actions_assume_role" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [data.aws_iam_openid_connect_provider.github_actions.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:adamrothman@662688/dustjacket@1384650842:ref:refs/heads/main"]
    }
  }
}

resource "aws_iam_role" "github_actions" {
  name               = "github-actions-${local.name}"
  assume_role_policy = data.aws_iam_policy_document.github_actions_assume_role.json
}

# Code only: the workflow uploads a new package and waits for the update
# to land. Configuration stays Terraform's (lambda.tf).
data "aws_iam_policy_document" "github_actions_lambda" {
  statement {
    actions = [
      "lambda:GetFunction",
      "lambda:GetFunctionConfiguration",
      "lambda:UpdateFunctionCode",
    ]
    resources = [aws_lambda_function.app.arn]
  }
}

resource "aws_iam_role_policy" "github_actions_lambda" {
  name   = "lambda-deploy"
  role   = aws_iam_role.github_actions.id
  policy = data.aws_iam_policy_document.github_actions_lambda.json
}
```

`outputs.tf`:

```hcl
# The role dustjacket's .github/workflows/deploy.yml assumes (its ARN is
# hard-coded there; the account ID is not a secret).
output "github_actions_role" {
  value = aws_iam_role.github_actions.arn
}

output "alerts_topic" {
  value = aws_sns_topic.alerts
}

# Sensitive because the origin's custom header carries the
# origin-verify secret.
output "distribution" {
  sensitive = true
  value     = aws_cloudfront_distribution.app
}

# Sensitive because the function's environment carries the
# origin-verify secret; `terraform output function` shows it on request.
output "function" {
  sensitive = true
  value     = aws_lambda_function.app
}

# Reachable, but the app refuses requests without CloudFront's
# X-Origin-Verify header, so the URL is only useful for confirming the
# function is up.
output "function_url" {
  value = aws_lambda_function_url.app
}

output "kms_key" {
  value = aws_kms_key.keys
}

output "table" {
  value = aws_dynamodb_table.app
}
```

`CLAUDE.md`:

~~~markdown
# acct-apps/dustjacket

This stack is Dustjacket, a remote MCP server that connects Claude to Hardcover: one Go binary on Lambda (arm64, function URL) behind CloudFront at https://dustjacket.rothman.tools, a DynamoDB single table, and a customer-managed KMS key that seals each person's Hardcover API key (the app calls Encrypt and Decrypt directly, with the Hardcover user ID as encryption context). There are no SSM secrets; the only shared secret is the CloudFront origin-verify value, generated here. `locals.allowed_users` is the allowlist of Hardcover usernames, passed to the function as `DUSTJACKET_ALLOWED_USERS`; adding someone is an edit and an apply. The `rothman.tools` zone belongs to `acct-apps/dns`. Deploys happen from the dustjacket repo's GitHub Actions via the `github-actions-dustjacket` role (`aws lambda update-function-code`), not from Terraform, which owns the function's configuration only; the repo's OIDC subject must use immutable IDs (see `github-actions.tf`).
~~~

- [ ] **Step 3: Fill in the allowlist**

In `locals.tf`:
- Replace `"ADAM_HARDCOVER_USERNAME"` with Adam's Hardcover username. It's in your Task 1 output (`$SCRATCHPAD/live.txt`, the `L6` line); confirm it with Adam.
- Replace `"SISTER_HARDCOVER_USERNAME"` with his sister's. Ask Adam for it.

Neither is a secret.

- [ ] **Step 4: Update the repo's CLAUDE.md**

In the layout list, after the `acct-apps/pickem` bullet, add:

```
- `acct-apps/dustjacket` — Dustjacket, a remote MCP server that connects Claude to Hardcover (Lambda behind CloudFront at https://dustjacket.rothman.tools, DynamoDB, KMS). Deploys happen from the dustjacket repo, not from Terraform. Details in `terraform/aws/acct-apps/dustjacket/CLAUDE.md`.
```

In the apply-order paragraph, change the part about `acct-apps/dns` so that it also says the dns stack creates the `rothman.tools` zone that `acct-apps/dustjacket` reads, and add `dustjacket` to the deploy roles that look up the OIDC provider. Keep the paragraph's existing wording otherwise. For example, `the sites/app/pickem deploy roles` becomes `the sites/app/pickem/dustjacket deploy roles`.

- [ ] **Step 5: Format, init, validate, plan**

```bash
cd terraform/aws/acct-apps/dustjacket
terraform fmt -check -diff .
terraform init
terraform validate
terraform plan -out=plan.tfplan
```

- If `init` or `plan` fails for credentials, ask Adam to run `! aws sso login --profile apps`.
- **Expected:** `fmt` prints nothing and `validate` says `Success! The configuration is valid.`
- **Expected plan:** `Plan: 24 to add, 0 to change, 0 to destroy.` That covers the KMS key and alias, the table, the log group, the Lambda role and its three policies, `random_password.origin_verify`, the function, its URL and two permissions, the certificate and its validation, the distribution, three Route 53 records, the SNS topic and subscription, the alarm, and the deploy role and its policy.
- Show Adam the plan's summary line and resource list. **Do not apply** until Task 14 Step 1.

- [ ] **Step 6: Commit and open a PR**

```bash
cd ~/src/personal-infra
git add CLAUDE.md terraform/aws/acct-apps/dustjacket/*.tf terraform/aws/acct-apps/dustjacket/CLAUDE.md terraform/aws/acct-apps/dustjacket/.terraform.lock.hcl
git commit -m "apps: add the dustjacket stack"
git push -u origin apps-dustjacket
gh pr create --title "apps: add the dustjacket stack" --body "<summary of the stack and the plan output; end with the PR attribution line from your session's instructions>"
```

Only the files named above are committed; `plan.tfplan` and `.terraform/` stay local.

---

### Task 14: First deploy and connect

Everything here is outward-facing: it creates AWS resources, changes repository settings, deploys, and involves real accounts. Get Adam's explicit go-ahead for Steps 1, 3 and 5.

- [ ] **Step 1: Apply (with Adam's approval)**

```bash
cd ~/src/personal-infra/terraform/aws/acct-apps/dustjacket && terraform apply plan.tfplan
```

- **Expected:** `Apply complete! Resources: N added, 0 changed, 0 destroyed.` The certificate validation can take a few minutes, and the CloudFront distribution 5–15.
- Then `rm plan.tfplan`.
- Tell Adam to confirm the SNS email subscription sent to `rothman.adam+dustjacket@gmail.com`.

- [ ] **Step 2: Check the placeholder answers**

Run `curl -sI https://dustjacket.rothman.tools/healthz | head -1`.
Expected: a 5xx. The placeholder bootstrap exits 1, which also fires the error alarm once. Tell Adam to expect that alarm email. A DNS or TLS failure instead means the zone's delegation or the certificate isn't in place yet: stop and check Task 13 Step 1.

- [ ] **Step 3: Let the deploy workflow in (with Adam's approval)**

```bash
gh api -X PUT repos/adamrothman/dustjacket/actions/oidc/customization/sub -F use_default=true -F use_immutable_subject=true
```

Expected: HTTP 201 with an empty body. `github-actions.tf` requires this for its `sub` condition.

- [ ] **Step 4: Open the PR**

```bash
cd ~/src/dustjacket
git push -u origin implement
gh pr create --base main --title "Dustjacket v1" --body "<what it is, the tasks done, the live-check findings in brief; end with the PR attribution line from your session's instructions>"
gh pr checks --watch
```

Expected: CI passes.

- [ ] **Step 5: Merge and deploy (with Adam's approval)**

Adam merges the PR, or approves you running `gh pr merge --squash`. Then:

```bash
gh run list --workflow deploy.yml --limit 1
gh run watch <run id> --exit-status
```

Expected: the deploy run succeeds, and its last step prints the function's new `LastModified`.

- [ ] **Step 6: Smoke-test**

```bash
curl -s https://dustjacket.rothman.tools/healthz; echo
curl -s https://dustjacket.rothman.tools/.well-known/oauth-protected-resource; echo
curl -si -X POST https://dustjacket.rothman.tools/mcp -H 'Content-Type: application/json' -d '{}' | grep -i -E '^HTTP|www-authenticate'
```

Expected:
- `ok`
- JSON with `"authorization_servers":["https://dustjacket.rothman.tools"]`
- `HTTP/2 401` and `www-authenticate: Bearer resource_metadata="https://dustjacket.rothman.tools/.well-known/oauth-protected-resource"`

- [ ] **Step 7: Adam connects from claude.ai**

Walk Adam through the runbook's "Connecting claude.ai". Then read the log:

```bash
aws --profile apps logs tail /aws/lambda/dustjacket --since 15m --format short | grep -E '"msg":"(request|connected|connect: )'
```

- **Expected:** `POST /oauth/register` 201, `GET /oauth/authorize` 200, `POST /oauth/authorize` 302 and a `connected` line, `POST /oauth/token` 200, then `/mcp` 200s.
- **This checks Review Focus 2.** An `/oauth/authorize` redirect carrying `invalid_scope` or `invalid_target` means claude.ai sent a `scope` or `resource` the parser refuses. Record what it sent (it's in Adam's browser address bar, or ask him), relax `ParseAuthorize` to accept it, add a decisions entry, and redeploy.

- [ ] **Step 8: Adam tries it**

In claude.ai, Adam asks something that reads, like "What am I reading right now?", and something that writes, like "I'm on page 120 of <book>".

Confirm:
1. Claude calls `hardcover_docs` before its first query.
2. Claude asks for approval before `hardcover_mutate` runs. This is the premise of decision 2. If claude.ai offers no per-tool approval, tell Adam and record it in `docs/decisions.md`.
3. The request log lines show `tool`, `op`, `fields` and `hc_status` for each call.
4. **Review Focus 1:** Adam, or his sister in Step 9, connects from a phone as well as a desktop browser. The phone connect must reach the `connected` log line.

- [ ] **Step 9: Adam's sister connects**

She follows the runbook's "Connecting claude.ai" with her own key. Check for her `connected` line in the log.

- [ ] **Step 10: Come back after an hour**

After more than an hour, Adam uses Dustjacket again. Claude should refresh its token without asking him to reconnect: the log shows `POST /oauth/token` 200, then `/mcp` 200. If Claude asks him to reconnect, look at the token endpoint's log lines, then report to Adam.

- [ ] **Step 11: Record what you learned**

Anything surprising about claude.ai or Hardcover gets:
- an entry in `docs/decisions.md`, or a fix to `docs/runbook.md`
- a correction to `docs/hardcover-notes.md` if it's about the API

Commit these on a branch and open a PR. Delete the Hardcover keys used for the live checks, if Adam no longer wants them:

```bash
! rm ~/.config/dustjacket/hardcover-token*
```

Then delete them on hardcover.app/account/api too.

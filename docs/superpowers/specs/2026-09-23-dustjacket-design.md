# Dustjacket: a remote MCP server for Hardcover

Design spec, 2026-09-23. Agreed with Adam in a brainstorming session; this
document is the reference for the implementation plan.

## Goal

Let Adam and his sister use their [Hardcover](https://hardcover.app)
accounts from claude.ai and the Claude apps, anywhere, through a remote MCP
server that Adam hosts. Each person connects with their own Hardcover API
key.

### What Adam asked for

- A remote MCP server usable from claude.ai and the Claude apps, unlike the
  local stdio prior art ([kristianedlund/hardcover-mcp](https://github.com/kristianedlund/hardcover-mcp)).
- Multiple users (Adam and his sister), each providing their own Hardcover
  API key, stored encrypted.
- AWS Lambda, DynamoDB, Go, and pickem's architecture where it fits.
- A small tool surface in the spirit of the AWS MCP server (one "call the
  API" tool plus documentation) rather than one tool per operation.

### Use cases (all in scope)

1. **Log reading:** set want-to-read / reading / read, update progress, rate,
   review.
2. **Ask about my library:** what I read and when, what's on my TBR, stats,
   goals.
3. **Discover books:** search the catalog; series, authors, editions; what
   friends are reading.
4. **Curate lists:** create and edit lists, goals, journal entries.
5. **Recommendations** drawn from reading history.

### Success criteria

- Adam and his sister each add the connector in their own claude.ai account,
  paste a Hardcover key once, and can then do all five of the above in
  conversation, including from their phones.
- Nobody outside the allowlist can connect.
- A stored Hardcover key is useless to someone who has only the DynamoDB
  table.
- It costs about $1.50 a month plus the domain.

### Hardcover's terms

Hardcover's getting-started page says the API is in beta and may change,
that keys may be reset without notice, that it must be called from a
backend and never a browser, and that public or commercial products may not
use data owned by users except on behalf of a user who allowed it.
Dustjacket calls it only from the server, only with each person's own key,
for an allowlisted few; it is neither public nor commercial.

## Decisions

These seed `docs/decisions.md` as its first numbered entries.

1. **Remote, on Lambda.** One Go binary behind CloudFront, following pickem.
2. **Three tools: GraphQL query, GraphQL mutation, docs.** Hardcover's API is
   a Hasura GraphQL API with ~1,300 types, 154 query and 108 mutation root
   fields. Curated tools (the prior art has 39) cost code, maintenance
   against a beta API, and context, and still don't cover open-ended
   questions. Reads and writes are separate tools so claude.ai's per-tool
   permissions can let reads run freely while writes ask first. A curated
   tool is added only if real use shows Claude repeatedly getting a
   particular write wrong.
3. **The Hardcover key is the identity.** No Google sign-in, no magic links:
   the connect page asks for a key, and Hardcover's `me` says whose it is.
4. **Allowlist by Hardcover username**, in the `DUSTJACKET_ALLOWED_USERS`
   environment variable set by Terraform. A renamed account is locked out
   until the list is updated (fails safe).
5. **Keys are encrypted with a customer-managed KMS key**, by direct
   `Encrypt`/`Decrypt` with the Hardcover user ID as encryption context. An
   AES key in SSM would save $1 a month but would let anyone holding the
   parameter and the table decrypt everything offline, unaudited.
6. **Keys live on the user, not the grant.** Reconnecting with a new key
   updates the user record, so every connection that person has picks it up.
7. **Unused OAuth clients expire.** Dynamic client registration is open, as
   MCP expects; a client nobody connects through within 24 hours is deleted
   by DynamoDB TTL. The first successful connect makes it permanent.
8. **No change notifications.** Tools are advertised without `listChanged`
   and there is no logging capability, for the reason in pickem's decision
   30: a buffered function URL cannot hold a stream open.
9. **No retries on Hardcover's 429.** The tool reports the wait and Claude
   decides; retrying inside a 29-second Lambda budget would mostly time out.
10. **Responses over 100 KB are refused** with a request for a narrower
    query, to protect the conversation's context. The threshold is a
    starting point.
11. **The key's scopes are pre-selected** on Hardcover's key page by the link
    the connect page shows (below): the smallest set covering the use cases.
12. **Hosted at `dustjacket.rothman.tools`.** `rothman.tools` is Adam's new
    domain for AI connectors, one subdomain per connector; its zone lives in
    `acct-apps` next to the functions it serves. The subdomain is the
    project's name, not Hardcover's, so it does not read as an official
    Hardcover service.
13. **The schema snapshot comes from Hardcover's docs repo**
    ([hardcoverapp/hardcover-docs](https://github.com/hardcoverapp/hardcover-docs),
    MIT), refreshed by a script, rather than live introspection: lookups are
    free, fast, and testable.
14. **No point-in-time recovery.** Losing the table means everyone
    reconnects; nothing else is lost.
15. **Logs never carry query text, variables, keys, or request bodies.**
    Mutations can carry reviews and private notes.

## Architecture

```
claude.ai / Claude apps
        │  HTTPS
        ▼
CloudFront  dustjacket.rothman.tools   (adds X-Origin-Verify)
        │
        ▼
Lambda function URL → one Go binary (arm64, provided.al2023)
   ├─ /.well-known/oauth-authorization-server
   ├─ /.well-known/oauth-protected-resource
   ├─ /oauth/register · /oauth/authorize (connect page) · /oauth/token · /oauth/revoke
   └─ /mcp  (stateless Streamable HTTP, JSON responses)
        │                         │
        ▼                         ▼
   DynamoDB (one table)      KMS key (encrypts stored Hardcover keys)
                                  │
                                  ▼
                   api.hardcover.app/v1/graphql (with that user's key)
```

The binary is pickem's shape without jobs, email, feeds, or a web app. On
Lambda it serves the function URL; locally `dustjacket serve [-addr :8080]
[-memory]` serves HTTP.

Carried over from pickem, adapted:

- The OAuth 2.1 authorization server (`internal/oauth`): authorization code
  with PKCE S256, opaque tokens stored hashed, 1-hour access tokens, 30-day
  rotating refresh tokens, RFC 8414 and RFC 9728 metadata, RFC 7591 dynamic
  registration restricted to redirect hosts `claude.ai` and `claude.com`,
  RFC 7009 revocation. One OAuth scope, `hardcover`.
- The MCP handler pattern (`internal/mcpserver`): authenticate the bearer
  token, then serve a per-request `mcp.Server` bound to that user, stateless
  with JSON responses, using `github.com/modelcontextprotocol/go-sdk`.
- The invocation budget: every request works under Lambda's deadline less 3
  seconds.
- The `X-Origin-Verify` check, so the public function URL cannot bypass
  CloudFront.
- The store interface and its in-memory twin (`internal/store`), trimmed to
  `Get`, `Put`, `PutIfAbsent`, `Delete`, `Transact`.
- One `request` log line per request.

There are no SSM secrets: OAuth tokens are random and stored hashed, Hardcover
keys are KMS-encrypted, and nothing uses cookies. The origin-verify value
comes from Terraform as an environment variable.

### Configuration

Environment variables, prefixed `DUSTJACKET_`:

| Variable | Meaning | Default |
|---|---|---|
| `BASE_URL` | Public origin, also the OAuth issuer | `http://localhost:8080` |
| `TABLE` | DynamoDB table name | `dustjacket` |
| `KMS_KEY_ID` | KMS key ARN for sealing Hardcover keys | none; required on Lambda |
| `ALLOWED_USERS` | Comma-separated Hardcover usernames, case-insensitive | none; nobody can connect |
| `ORIGIN_VERIFY` | Shared secret CloudFront sends as `X-Origin-Verify` | unset: check disabled (local) |
| `OAUTH_REDIRECT_HOSTS` | Hosts a registered redirect URI may use | `claude.ai,claude.com` |
| `HARDCOVER_URL` | GraphQL endpoint | `https://api.hardcover.app/v1/graphql` |

`serve -memory` uses the in-memory store and the local sealer (below), so
local runs need no AWS access.

## Connecting

1. In claude.ai, the person adds a custom connector with URL
   `https://dustjacket.rothman.tools/mcp`. claude.ai gets a 401 whose
   `WWW-Authenticate` names the protected-resource metadata, registers a
   client, and opens `/oauth/authorize`.
2. `GET /oauth/authorize` validates the request (known, unexpired client;
   registered redirect URI; `response_type=code`; PKCE S256; scope empty or
   `hardcover`; `resource`, if given, equal to `<BASE_URL>/mcp`) and renders
   the connect page:
   - What Dustjacket is, that it is not affiliated with Hardcover, and the
     name of the client asking to connect.
   - Step 1: **Create a Hardcover API key**, a link opening in a new tab:
     `https://hardcover.app/account/api/keys/new?scope=read:me:content+read:catalog+read:library+write:library+read:journal+read:lists+write:lists+read:goals+write:goals+read:social+read:users+write:reviews`
   - Step 2: a password field for the key, and **Connect**.
   - The OAuth parameters ride along as hidden fields. The page is plain
     HTML with one stylesheet, no script, under `default-src 'self'`.
3. `POST /oauth/authorize`:
   - Rejects the post unless its `Origin` header equals `BASE_URL`'s origin.
   - Re-runs the step 2 validation on the posted parameters.
   - Trims the key and strips a leading `Bearer ` if pasted.
   - Calls Hardcover `query { me { id username } }` with the key (`me`
     returns an array; its first element is the user).
   - On failure, re-renders the page with the parameters kept, the key field
     empty, and one message:

     | Outcome | Message |
     |---|---|
     | 401, or `me` empty | Hardcover didn't accept that key. Check that you copied all of it and that it hasn't expired. |
     | 403 `insufficient_scope` | That key can't read your Hardcover profile. Create one with the link above. |
     | Username not allowlisted | @username isn't on this server's allowlist. Ask Adam to add you. |
     | Timeout, 5xx, network | Couldn't reach Hardcover. Try again in a moment. |

   - On success: upserts the user record with the sealed key, creates a grant
     for this client, clears the client's expiry, issues an authorization
     code, and redirects to the client's redirect URI with `code` and
     `state`.
4. `/oauth/token` and `/oauth/revoke` behave as in pickem, except that an
   expired client is treated as unknown.

### Scopes

The key's scopes, from Hardcover's published scope map
(`https://api.hardcover.app/capabilities.json`, 2026-09-23):

| Scope | For |
|---|---|
| `read:me:content` | `me` (id, username) at connect time and in queries |
| `read:catalog` | `search`, books, editions, authors, series, trending |
| `read:library`, `write:library` | `user_books`, `user_book_reads`, their inserts and updates, `edition_owned`, journal inserts |
| `read:journal` | `reading_journals` |
| `read:lists`, `write:lists` | lists and list books |
| `read:goals`, `write:goals` | reading goals |
| `read:social` | `activities`, `followed_users`, friends' reading |
| `read:users` | other users' public profiles |
| `write:reviews` | reviews; the scope map lists no operations for it today (see live check L5) |

Deliberately excluded: `read:me:email`, `read:me:roles`, `read:account`,
notifications, prompts, vibes, `write:social` (follow, like, block), and
`write:catalog*` (librarian edits). A tool call that needs a missing scope
gets Hardcover's `insufficient_scope` error naming it, with the key link.

## Token storage

`internal/sealer` defines:

```go
type Sealer interface {
	Seal(ctx context.Context, userID string, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, userID string, ciphertext []byte) ([]byte, error)
}
```

- **KMS** (Lambda): `Encrypt` and `Decrypt` against `KMS_KEY_ID` with
  encryption context `{"hardcover_user_id": userID}`. A ciphertext moved onto
  another user's record does not decrypt. The Lambda role may use this key
  only, for `kms:Encrypt` and `kms:Decrypt`; every decrypt is in CloudTrail.
- **Local** (tests, `serve -memory`): AES-256-GCM with a random per-process
  key, with the user ID as additional authenticated data, so the
  wrong-user-fails behavior is tested for real.

The plaintext key exists only in memory for the request that uses it. It is
never logged, returned, or stored.

## MCP tools

Server instructions (sent to Claude on connect), in substance:

> Dustjacket gives you the Hardcover API (GraphQL) as the connected user.
> Call `hardcover_docs` with no arguments before your first query in a
> conversation, and read the relevant guide before any mutation. Use
> `hardcover_query` to read and `hardcover_mutate` to write. Reading
> statuses: 1 Want to Read, 2 Currently Reading, 3 Read, 4 Paused, 5 Did Not
> Finish, 6 Ignored. Names match only through `search` (`_like`, `_ilike` and
> regex operators are disabled). Each request may have at most 5 top-level
> fields, or 1 `search`; each top-level field counts against 60 requests a
> minute.

| Tool | Input | Annotations | Check before sending |
|---|---|---|---|
| `hardcover_query` | `query` (string), `variables` (object, optional), `operation_name` (string, optional) | `readOnlyHint: true`, `openWorldHint: true` | The document parses, and every operation in it is a query |
| `hardcover_mutate` | same | `readOnlyHint: false`, `destructiveHint: true`, `idempotentHint: false`, `openWorldHint: true` | The document parses, and every operation in it is a mutation |
| `hardcover_docs` | `topic` (string, optional) or `name` (string, optional) | `readOnlyHint: true` | — |

Subscriptions are rejected by both GraphQL tools. Parsing uses
`github.com/vektah/gqlparser/v2` without schema validation; Hardcover
validates.

### Results of the GraphQL tools

A single text content block holding JSON:

```json
{"rate_limit": "<RateLimit header value, verbatim>", "response": <Hardcover's JSON body>}
```

The result is flagged `isError` when Hardcover's body has a non-empty
`errors`. A serialized result over 100 KB is replaced by an error: "The
response was N KB, over the 100 KB limit. Ask for fewer fields or add a
`limit`."

### `hardcover_docs`

- No arguments: the overview: what each tool is for, the topic list, status
  and privacy IDs (privacy: 1 Public, 2 Followers, 3 Private), limits, and
  the known traps.
- `topic`: one embedded markdown guide: `library`, `reading`, `search`,
  `catalog`, `lists`, `goals`, `journal`, `social`, `recommendations`,
  `limits`. Each guide gives the shapes to use, worked queries and
  mutations, and the traps confirmed by the live checks. Unknown topics get
  the list.
- `name`: every match for that name: the SDL of a type so named, and the
  arguments and return type of a root field so named, since Hasura often
  uses one name for both (`books` is an object type and a query root
  field). `query_root.<field>` or `mutation_root.<field>` narrows to one
  root field. Examples: `user_books`, `update_user_book`,
  `mutation_root.insert_user_book`, `UserBookUpdateInput`. Unknown names get
  up to five suggestions ranked by similarity.
- Both `topic` and `name`: an error saying to give one.

The schema snapshot (`internal/reference/schema.graphql`, about 580 KB) and
the guides are embedded with `go:embed`. `scripts/update-schema.sh` refreshes
the snapshot from
`https://raw.githubusercontent.com/hardcoverapp/hardcover-docs/main/schema.graphql`,
and the MIT notice travels with it.

## Hardcover client

`internal/hardcover`: POST `{query, variables, operationName}` to
`HARDCOVER_URL` with `Authorization: Bearer <key>`, `Content-Type:
application/json`, and `User-Agent: dustjacket/<version>
(+https://dustjacket.rothman.tools)`, under a 20-second timeout inside the
invocation budget.

| Hardcover | Tool result (`isError: true` unless noted) |
|---|---|
| 200 | Body as above; `isError` only when `errors` is non-empty |
| 401 `invalid_token` | "Hardcover rejected your API key (expired or revoked). In Claude's settings, disconnect and reconnect Dustjacket to paste a new one." |
| 403 `insufficient_scope` | "Your Hardcover key lacks the `<scope>` scope. Create a new key with the link on the connect page, then reconnect Dustjacket." |
| 403 `top_level_limit_exceeded` | "Hardcover allows at most 5 top-level fields per request, or 1 `search`. Split the request." |
| Other 403 (e.g. `unsupported_operation`) | Hardcover's error body, verbatim |
| 429 | "Rate limited by Hardcover; retry after N seconds." (from `Retry-After`) |
| 408, 5xx, timeout, network | "Hardcover timed out or is unavailable; this is safe to retry." |

## Data model

One DynamoDB table, `dustjacket`: `PK` and `SK` strings, a `type` attribute,
TTL on `ttl`, on-demand billing, deletion protection on, no secondary
indexes, no point-in-time recovery.

| PK | SK | Attributes | TTL |
|---|---|---|---|
| `USER#<hardcover_user_id>` | `META` | `user_id`, `username`, `key_ciphertext` (binary), `key_updated_at`, `created_at` | — |
| `OAUTHCLIENT#<id>` | `META` | `id`, `name`, `redirect_uris`, `secret_hash` (optional), `created_at`, `expires` (optional) | `expires`, until first connect |
| `GRANT#<id>` | `META` | `id`, `client_id`, `client_name`, `user_id`, `created_at` | — |
| `AUTHCODE#<hash>` | `META` | `client_id`, `redirect_uri`, `challenge`, `grant_id`, `expires` | 10 minutes |
| `TOKEN#<hash>` | `META` | `kind` (access or refresh), `grant_id`, `client_id`, `issued`, `expires` | 1 hour or 30 days |

- Registration writes the client with `expires` 24 hours out. A successful
  connect writes the client again without `expires`, in the same
  transaction as the grant and the authorization code. Reads treat an
  expired client as absent, since TTL deletion lags.
- An MCP call reads token, grant, user (three `GetItem`s) and makes one KMS
  `Decrypt`.
- Revoking a token deletes its grant, which kills that connection's tokens.
  Deleting a user record breaks every connection that person has.

## Logging and operations

- One `request` line per request: method, path, status, `ms`; on `/mcp` also
  the MCP method, tool name, operation type, top-level field names,
  Hardcover's HTTP status, and Hardcover's latency. Never query text,
  variables, keys, request bodies, or query strings (which carry OAuth
  codes).
- Lambda timeout 29 seconds (below CloudFront's 30), work budget ends 3
  seconds before it, 90-day log retention, an error alarm that emails Adam.

## Infrastructure (personal-infra)

On a branch in `personal-infra`, following the pickem stack's conventions:

- `terraform/aws/acct-apps/dns`: a `rothman.tools` hosted zone
  (`prevent_destroy`), with its name servers output for the registrar.
- `terraform/aws/acct-apps/dustjacket`: KMS key (with rotation enabled) and
  alias; DynamoDB table; Lambda (arm64, `provided.al2023`, 29 s, placeholder
  bootstrap, code ignored by Terraform); function URL (auth `NONE`,
  buffered); CloudFront distribution with ACM certificate (us-east-1) and
  the origin-verify header; Route 53 alias; log group; errors alarm;
  `github-actions-dustjacket` deploy role; IAM scoped to the table, the key,
  and the log group. `locals.allowed_users` holds the allowlist.

## Testing

Tests are written first and drive the real handlers, sending the shapes
real clients send.

- `store`: the in-memory twin shares the DynamoDB encoding; conditional
  writes and TTL-bearing items are covered.
- `sealer`: round trip, and open-with-wrong-user fails, on the local sealer.
- `hardcover`: an `httptest` fake of the GraphQL endpoint asserting method,
  headers, and body; a table test over every row of the error mapping.
- `oauth`: the whole flow over `httptest`: register, connect page, post a
  key (fake Hardcover `me`), allowlisted and not, bad key, missing scope,
  wrong origin, code exchange, refresh rotation, revoke, client expiry
  before and after connect.
- `mcpserver`: a real go-sdk MCP client with a test-minted token: tool list
  and annotations, query rejects mutations and mutate rejects queries,
  subscriptions rejected, the 100 KB cap, error mapping, the docs tool's
  overview, topics, name lookup, and suggestions.
- `reference`: every guide the overview lists exists, and every GraphQL
  example in every guide validates against the schema snapshot, so a
  schema refresh that breaks a guide fails CI.
- Live tests (`-tags live`, `HARDCOVER_TOKEN` set, run by hand, never in CI)
  against the real API, encoding the live checks below.

CI (`.github/workflows/ci.yml`): `gofmt -l`, `go vet`, `go test ./...`, and
the arm64 Lambda build. Deploy (`.github/workflows/deploy.yml`): on push to
`main`, test, build, `aws lambda update-function-code` via the deploy role.

## Live checks

Run against the real API with Adam's key before the guides are written, on a
book Adam chooses, cleaning up afterwards. Findings are recorded with dates
in `docs/hardcover-notes.md` and later become live tests.

| # | Question | What the answer changes |
|---|---|---|
| L1 | Does `update_user_book` with a partial `object` null the fields left out? | The library guide says "send only what changes" or "read, merge, send everything" |
| L2 | Is `edition_owned` a toggle? | The library guide warns and shows checking first, or not |
| L3 | Does `user_books` without a `where` return only the caller's rows? | Whether every guide must filter on `user_id` |
| L4 | What do `search` results look like for each `query_type`? | The search guide's shapes and hydration recipe |
| L5 | Can a review be written with `write:library` alone? | Whether `write:reviews` stays in the scope list |
| L6 | Does `me` work with only `read:me:content`? | The connect flow's single required scope |
| L7 | Does `progress_seconds` persist without an audiobook `edition_id`? | The reading guide |
| L8 | Does a journal entry with `privacy_setting_id: 0` fail silently? | The journal guide |

## Repository

```
cmd/dustjacket/        main.go (Lambda entry, serve), config.go
internal/store/        interface, DynamoDB, in-memory twin
internal/sealer/       KMS and local sealers
internal/hardcover/    GraphQL client and error mapping
internal/oauth/        authorization server, clients, grants, users
internal/web/          routes, connect page template and stylesheet, origin check, request log
internal/mcpserver/    /mcp handler and the three tools
internal/reference/    schema snapshot, guides, lookup
scripts/               update-schema.sh
docs/                  decisions.md, runbook.md, hardcover-notes.md
```

`CLAUDE.md` and `README.md` follow pickem's: what it is, commands, how it
fits together, conventions. The runbook covers: connecting claude.ai,
adding someone to the allowlist, an expired key, revoking access (including
the `aws dynamodb delete-item` for a user record), refreshing the schema
snapshot, and what to do when the alarm fires.

## Delivery order

1. Live checks (L1–L8), recorded in `docs/hardcover-notes.md`.
2. Scaffold, `store`, `sealer`.
3. `hardcover` client, with the live tests.
4. OAuth and the connect page.
5. MCP tools, `reference`, and the guides written from step 1.
6. Terraform in personal-infra.
7. First deploy; Adam connects from claude.ai; his sister connects.

Inputs needed from Adam along the way: a Hardcover key for the live checks,
his sister's Hardcover username, and the registrar change pointing
`rothman.tools` at the new zone's name servers.

## Cost

About $1.50 a month plus the domain: $1.00 for the KMS key and $0.50 for the
hosted zone (shared with future connectors). At about 10,000 invocations a
month, Lambda, DynamoDB, CloudFront, KMS requests, and logs each come to
cents or nothing.

## Out of scope

- Curated per-operation tools (decision 2 says when one would be added).
- A web page for managing connections or keys, and an admin page.
- Claude Code as a client: its OAuth callback is on `localhost`, which the
  redirect-host restriction excludes. Adding `localhost` to
  `OAUTH_REDIRECT_HOSTS` would allow it.
- OAuth Client ID Metadata Documents, until claude.ai's support is
  confirmed.
- Caching decrypted keys across requests.

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Dustjacket, a remote MCP server that lets claude.ai and the Claude apps use a person's [Hardcover](https://hardcover.app) account: one Go binary on Lambda behind CloudFront at https://dustjacket.rothman.tools. Each person connects with their own Hardcover API key, which is sealed with KMS and stored in DynamoDB; the admins (Hardcover user IDs in configuration) and an allowlist in the table, which admins manage from Claude, decide who may connect. `README.md` has the layout and how to run it; `docs/superpowers/specs/2026-09-23-dustjacket-design.md` is the original design; `docs/decisions.md` is the numbered record of every design choice since, and is the authority where the two differ; `docs/runbook.md` is how Adam operates it; `docs/hardcover-notes.md` is what the live checks found about Hardcover's API. Infrastructure is Terraform in the `personal-infra` repo (`terraform/aws/acct-apps/dustjacket`); deploys happen from this repo's `main` via `.github/workflows/deploy.yml`, which runs the Test workflow (`.github/workflows/test.yml`) first and deploys only once it passes.

## Commands

```sh
go test ./...                                   # everything; in-memory store, local sealer, fake Hardcover
gofmt -l . && go vet ./...                      # the Test workflow fails on either
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dev/null ./cmd/dustjacket   # the Lambda build the Test workflow checks
go run ./cmd/dustjacket serve -memory           # local server, no AWS
./scripts/update-schema.sh                      # refresh the schema snapshot, then go test ./internal/reference/
HARDCOVER_TOKEN=... HARDCOVER_TEST_BOOK_ID=... go test -tags live -v -count=1 ./internal/hardcover/   # live checks, by hand only
```

Configuration is `DUSTJACKET_*` environment variables (`cmd/dustjacket/config.go`). There are no secrets in SSM: OAuth tokens are random and stored hashed, Hardcover keys are KMS-sealed, and the origin-verify value comes from Terraform.

## How it fits together

- **Tools** (`internal/mcpserver`): `hardcover_query` (read-only; the server parses the document and refuses anything but queries), `hardcover_mutate` (mutations only), `hardcover_docs` (the overview, a topic guide, or a schema lookup). Admins alone also get `allowlist_list`, `allowlist_add` (looks the username up on Hardcover with the admin's key) and `allowlist_remove` (also deletes the person's user record and sealed key). The GraphQL tools pass Hardcover's JSON back verbatim (no HTML escaping) with its `RateLimit` header, refuse results over 100 KB, and turn Hardcover's refusals into messages Claude can act on; for a mutation, a timeout or oversized result never says "safe to retry", since the change may have happened. Stateless Streamable HTTP with JSON responses and no `listChanged`, since a buffered function URL cannot hold a stream open.
- **Reference** (`internal/reference`): the guides Claude reads (`guides/*.md`, written from the live checks) and lookups in `schema.graphql`, a snapshot from Hardcover's docs repository. Every GraphQL example in a guide is validated against the snapshot by the tests.
- **Hardcover** (`internal/hardcover`): a small GraphQL client (20-second timeout, 4 MB read cap), `Me`, `UserByUsername`, and `Scopes`/`KeyURL`, the key scopes the connect page pre-selects.
- **Auth** (`internal/oauth`): an OAuth 2.1 server with PKCE, dynamic client registration restricted to claude.ai and claude.com, hashed opaque tokens (1-hour access, rotating 30-day refresh), and clients that expire after a day unless someone connects through them. There is no login: the connect page (`internal/web`) takes a Hardcover key and asks Hardcover's `me` whose it is, and `Connect` checks that they are an admin (`DUSTJACKET_ADMIN_IDS`) or on the allowlist, then records the user (key sealed), the grant and the code in one transaction, conditioned on the allowlist not having changed. `Authenticate` and token refreshes check the same by user ID, reading the allowlist every time, so removing someone cuts off existing connections. Keys live on the user record, so reconnecting updates every connection.
- **Store** (`internal/store`): one DynamoDB table (`USER#`, `OAUTHCLIENT#`, `GRANT#`, `AUTHCODE#`, `TOKEN#`, and the one `ALLOWLIST` item, each with SK `META`), TTL on `ttl`; transactions put, delete and check (`Op.Put`, `Op.Delete`, `Op.Check`), and writes that must not race are conditional (`Op.IfAbsent`, `Op.IfMatch`, such as on the allowlist's `version`); `store.Memory` is the in-memory twin the tests use.
- **Sealer** (`internal/sealer`): KMS `Encrypt`/`Decrypt` with the Hardcover user ID as encryption context; `Local` (AES-GCM, user ID as AAD) for tests and `serve -memory`. Every sealed key records the key that sealed it (`key_sealed_by`); the sealer also opens with previous keys (`DUSTJACKET_KMS_PREVIOUS_KEY_IDS`, key ARNs) and says so, and `Authenticate` then seals it again under the current key with a write conditioned on `key_updated_at`, so a reconnect in the meantime wins.
- **Logging**: one `request` line per request (`internal/web`); the MCP tools add their fields through `internal/reqlog`. Never query text, variables, keys, bodies or query strings.
- **Timeouts**: every Lambda invocation works under a budget ending 3 s before the function's 29 s timeout, which sits below CloudFront's 30 s.

## Conventions

- This file must stay true. A change that contradicts anything here updates it in the same commit, as it does `README.md`, `docs/decisions.md` and `docs/runbook.md` where the change touches what they describe.
- A change to how Dustjacket works gets a new numbered entry in `docs/decisions.md` (or amends the entry it revises, saying so).
- Never state something about Hardcover's API that isn't in its docs, its schema, or `docs/hardcover-notes.md`. When a guide needs a fact nobody has checked, add a live check and run it.
- Tests drive the real handlers: the OAuth flow over `httptest` with the form a browser posts, the MCP tools through a real go-sdk client, Hardcover through a fake server. Test the shape the client actually sends.
- Secrets and private data (keys, query text, variables, reviews and notes, request bodies, query strings) never enter the repo or the logs, and neither does the AWS account ID (decision 25). Hardcover usernames, which are public, appear in the logs; the repo names no one, tests included: tests use made-up usernames and IDs.

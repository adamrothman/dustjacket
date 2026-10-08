# dustjacket

A remote MCP server that connects Claude to [Hardcover](https://hardcover.app),
at [dustjacket.rothman.tools](https://dustjacket.rothman.tools). Each
person connects with their own Hardcover API key; Claude then gets three
tools: `hardcover_query` and `hardcover_mutate`, which run Hardcover's
GraphQL API as that person, and `hardcover_docs`, guides and schema
lookups that tell Claude how. Admins also get three tools that manage who
may connect. One Go binary on AWS Lambda, DynamoDB, KMS.

`docs/superpowers/specs/2026-09-23-dustjacket-design.md` is the design,
`docs/decisions.md` the record of decisions since, `docs/runbook.md` how
it is operated, and `docs/hardcover-notes.md` what the live checks found
about Hardcover's API.

## Layout

| Path | What |
| --- | --- |
| `cmd/dustjacket` | The binary: Lambda entry point and `serve`; configuration |
| `internal/mcpserver` | `/mcp`, the three tools, and the admins' allowlist tools |
| `internal/reference` | Guides for Claude, the schema snapshot, lookups |
| `internal/hardcover` | GraphQL client, `Me`, `UserByUsername`, the key scopes; live checks (`-tags live`) |
| `internal/oauth` | OAuth 2.1 authorization server, users, grants, the allowlist |
| `internal/web` | Routes, the connect page, middleware |
| `internal/sealer` | KMS and local sealing of Hardcover keys |
| `internal/store` | DynamoDB single table and its in-memory twin |
| `internal/reqlog` | Log fields from the tools to the request's log line |
| `scripts/update-schema.sh` | Refreshes the schema snapshot |

## Run it locally

```sh
go test ./...
DUSTJACKET_ADMIN_IDS=<your Hardcover user ID> go run ./cmd/dustjacket serve -memory
```

## Deploy

Pushes to `main` run `.github/workflows/deploy.yml`: the Test workflow
(`.github/workflows/test.yml`: format, vet, test, the Lambda build), then,
only once it passes, build the arm64 binary and `aws lambda
update-function-code` via the `github-actions-dustjacket` role, whose ARN
is the `AWS_ROLE_ARN` Actions secret. Terraform (`personal-infra`, `acct-apps/dustjacket`) owns the
function's configuration and ignores its code. A re-run deploys only
when whoever started the run re-runs it.

## Security

Report a vulnerability privately; see `.github/SECURITY.md`.

## License

MIT; see `LICENSE`.

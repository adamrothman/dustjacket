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
   `hardcover_docs`. The connector syncs to the Claude phone apps; there is
   no separate phone connect. Keep approving `hardcover_mutate` call by call rather
   than always allowing it: Hardcover returns other people's reviews and
   list descriptions, which are text Claude reads while a write tool is
   available. Admins also get `allowlist_list`, `allowlist_add` and
   `allowlist_remove`; approve the last two call by call too.

The connect page has only been used from desktop browsers so far. The
first person to connect from a phone is the first real check of its
origin test (a refused form says "This form has to be sent from
Dustjacket's own page"); look for their `connected` log line.

## Adding someone

1. Ask for their Hardcover username.
2. In Claude, connected as an admin, ask to add them to Dustjacket's
   allowlist. `allowlist_add` looks the username up on Hardcover and adds
   their user ID; it is live at once. `allowlist_list` shows who is on the
   list.
3. Send them the steps under "Connecting claude.ai".

Someone not on the list who tries to connect sees "@username isn't on
this server's allowlist". The list matches user IDs, so renaming a
Hardcover account changes nothing (decision 23).

If `allowlist_add` can't find a username that is spelled right, their
account may be hidden from the admin's key. Have them try to connect: the
`connect: not on the allowlist` log line has their `user_id`. Then add
them by hand:

```sh
aws --profile apps dynamodb update-item --table-name dustjacket \
  --key '{"PK":{"S":"ALLOWLIST"},"SK":{"S":"META"}}' \
  --update-expression 'SET #users.#id = :u, #v = #v + :one' \
  --condition-expression 'attribute_exists(PK)' \
  --expression-attribute-names '{"#users":"users","#id":"<user id>","#v":"version"}' \
  --expression-attribute-values '{":one":{"N":"1"},":u":{"M":{"user_id":{"S":"<user id>"},"username":{"S":"<username>"},"added_at":{"S":"<now, e.g. 2026-10-07T00:00:00Z>"},"added_by":{"S":"<your username>"}}}}'
```

The condition fails only if the list has never been written; add anyone
with the tool first, then retry.

## Admins

Admins are the Hardcover user IDs in `locals.admin_ids` in the stack's
`locals.tf`, passed as `DUSTJACKET_ADMIN_IDS`. Adding or removing one is
an edit there and an apply. An admin may always connect, is never on the
allowlist, and is the only one who gets the allowlist tools. To find
someone's ID, ask Claude to look up their username with `hardcover_query`
(`users(where: {username: {_eq: "<username>"}}) { id }`).

## A key expired or was revoked

Every tool call then says "Hardcover rejected your API key". The person
disconnects and reconnects Dustjacket in Claude's settings, and pastes a
new key. That updates their stored key for all of their connections.

## Changing the key's scopes

A change to `hardcover.Scopes` reaches only keys made after it. Everyone
else keeps working until they ask for something their key can't do; then
Claude relays the refusal, which names the missing scope and links a new
key. To switch sooner, the person creates a key from the connect page's
link, then disconnects and reconnects Dustjacket in Claude's settings.
Tell everyone on the allowlist when the scopes change.

## Revoking access

Any of these, from quickest to most thorough:

- The person deletes the key at hardcover.app/account/api. Nothing
  Dustjacket holds works after that.
- The person removes the connector in Claude.
- An admin asks Claude to remove them from Dustjacket's allowlist.
  `allowlist_remove` takes them off the list and deletes their user
  record, sealed key included, which breaks all of their connections at
  once.
- For an admin: take their ID out of `locals.admin_ids` and apply, then
  delete their user record:

  ```sh
  aws --profile apps dynamodb delete-item --table-name dustjacket \
    --key '{"PK":{"S":"USER#<hardcover user id>"},"SK":{"S":"META"}}'
  ```

## Rotating or replacing the KMS key

The key (`alias/dustjacket`) does not rotate on its own (decision 17).

- **New key material, same key:** `aws --profile apps kms
  rotate-key-on-demand --key-id alias/dustjacket`, or set
  `enable_key_rotation` in the stack and apply. Nothing else changes: new
  seals use the new material, and KMS decrypts existing ciphertexts with
  the material that sealed them. Stored keys stay under the old material
  until each person reconnects.
- **A different key** (another account or region, or retiring this one):
  every stored key records the key that sealed it (decision 18), so this
  needs no downtime and nobody reconnects.
  1. In the stack, add the new key beside the old one. Give the Lambda
     role `kms:Encrypt` and `kms:Decrypt` on the new key and keep
     `kms:Decrypt` on the old. Set `DUSTJACKET_KMS_KEY_ID` to the new key's
     ARN and `DUSTJACKET_KMS_PREVIOUS_KEY_IDS` to the old one's, and apply.
     New connections seal under the new key; stored keys still open under
     the old one and are sealed again under the new one when next used.
  2. Check who is still on the old key:

     ```sh
     aws --profile apps dynamodb scan --table-name dustjacket \
       --filter-expression "begins_with(PK, :u) AND key_sealed_by = :old" \
       --expression-attribute-values '{":u":{"S":"USER#"},":old":{"S":"<old key ARN>"}}' \
       --projection-expression "user_id, username"
     ```

     (`name`, `type` and `ttl`, attributes elsewhere in the table, are
     DynamoDB reserved words: in a hand-written expression they need a
     placeholder, e.g. `--expression-attribute-names '{"#t":"type"}'`.)

     Anyone listed hasn't used Dustjacket since step 1. Wait for them, move
     them with `aws kms re-encrypt` (the same `hardcover_user_id=<id>`
     encryption context on both sides), writing back `key_ciphertext` and
     `key_sealed_by`, or delete their record so they reconnect.
  3. When nobody is left, empty `DUSTJACKET_KMS_PREVIOUS_KEY_IDS`, remove
     the old key's grant, apply, and schedule the old key's deletion.
- **A suspected leak:** neither of the above helps, since the old material
  still decrypts what it sealed. The secret that matters is the Hardcover
  key: the person deletes it at hardcover.app/account/api and reconnects
  with a new one. Every `Decrypt` of the KMS key is in CloudTrail.

## Refreshing the schema snapshot

```sh
./scripts/update-schema.sh
go test ./internal/reference/
```

If the tests fail, a guide's example no longer matches the schema: fix the
guide, then commit the snapshot and the guide together.

## Re-running the live checks

When Hardcover changes something, or before relying on a trap the guides
describe, rerun the checks in `docs/hardcover-notes.md`. They need a key
made from the connect page's link, saved at
`~/.config/dustjacket/hardcover-token` (copy it, then
`pbpaste > ~/.config/dustjacket/hardcover-token`), and a book that is not
in the library. Find the book's ID with the search check:

```sh
HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) HARDCOVER_SEARCH="<title and author>" \
go test -tags live -run TestLiveReads -v -count=1 ./internal/hardcover/
```

Pick the match with authors and readers; stubs can outrank it. Then:

```sh
HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) \
HARDCOVER_TOKEN_NO_REVIEWS=$(cat ~/.config/dustjacket/hardcover-token-no-reviews) \
HARDCOVER_TEST_BOOK_ID=<book id> \
go test -tags live -v -count=1 ./internal/hardcover/
```

- `HARDCOVER_TOKEN_NO_REVIEWS` is optional: a second key with only
  `read:me:content`, `read:library` and `write:library`, for L5.
- `HARDCOVER_CHECK_OWNED=1` also runs L2, which posts a public "added to
  Owned" activity, and L20, which deletes it.
- `TestLiveSocial` (L16–L19) follows, likes and blocks, and undoes each.
  `HARDCOVER_TEST_USER` is the username of someone the key's owner
  doesn't follow and who won't mind a follow and a like;
  `HARDCOVER_TEST_BLOCK_USER`, a stranger, with no follows either way, to
  block and unblock. L17 follows the test book's first author.
- The checks pace their requests a second apart to stay under Hardcover's
  burst of 10, so a full run takes about two minutes. `TestLiveFinish`
  (L11–L15, what finishing a book does to its reads) adds and removes the
  test book several times.

They add the test book to the library, privately, and remove it and its
journal entries again; the last log line (`cleanup: left behind`) should
show nothing. Update the notes and the guides with anything that changed.

## When the error alarm fires

`dustjacket-lambda-errors` emails when an invocation fails outright (a
crash, or start-up failing, for example a missing
`DUSTJACKET_KMS_KEY_ID`). Everything is in the `/aws/lambda/dustjacket`
log group:

- Every request logs one `request` line when it finishes: method, path,
  status, `ms`; on `/mcp` also the user, the tool, the operation type, the
  top-level fields, and Hardcover's status (`hc_status`, 0 for a timeout)
  and latency (`hc_ms`). Never the query or its variables.
- `connect: key rejected` and `connect: not on the allowlist` (with the
  person's `username` and `user_id`) are the connect page turning someone
  away; `connected` is a success.
- The allowlist tools' `request` lines say who changed what: `user` is
  the admin, `target` and `target_id` the person, and `result` what
  happened (`added`, `already`, `admin`, `no_such_user`, `removed`,
  `not_listed`).
- `authenticate` at error level is a store or KMS failure while checking a
  token: look at the error for which.
- A `REPORT` line marked `Status: timeout` means something ignored its
  context and Lambda killed it at 29 seconds.

## Dependency updates

Dependabot (`.github/dependabot.yml`) opens PRs on Mondays: one for the Go
modules' minor and patch bumps, one per Go major, and one for every GitHub
Actions bump, majors included, each proposed only once its release is a
week old. Security updates open as soon as Dependabot raises an alert. The
Test workflow runs on every PR, and merging one deploys it like any other
push to `main`, once the Test workflow passes again there. It does not run
`configure-aws-credentials`, which only the Deploy workflow uses, so a bump
to it is first tried by the deploy after the merge; if that fails, the
function keeps its old code.

## Repository collaborators

A collaborator on this repository has write access: GitHub offers no
other role on a repository owned by a personal account. What keeps that
from reaching production:

- The "Only Adam updates main" ruleset: they can push branches and open
  pull requests, but not merge them or push to `main`.
- The "Signed commits" ruleset covers every branch, so they need commit
  signing set up (an SSH signing key is simplest) before GitHub accepts a
  push.
- The deploy role trusts only runs on `main` started by Adam, and the
  Deploy workflow deploys on a re-run only when the run's starter re-runs
  it (decision 24), so they can't deploy or roll back.
- Settings, rulesets, secrets and collaborators are the owner's alone.
  The one Actions secret, `AWS_ROLE_ARN`, is the deploy role's ARN, which
  is no credential (decision 25); a workflow on a collaborator's branch
  could read it, and could read any other secret added, so add none.

What the rulesets can't check, read before merging:

- A pull request runs its own copy of `test.yml`, so a green `test` check
  says nothing about a pull request that changes `.github/`. Merging
  deploys within minutes.
- They can push to any branch but `main`, including an agent's or
  Dependabot's open pull request; the pull request's commit list shows
  who made and signed each commit.
- Their pull request comments reach agent sessions that watch that pull
  request, as input like anyone else's.

They can also push tags, create releases, delete branches other than
`main`, and run workflows on their branches, which cost nothing on
GitHub's standard runners, since the repository is public. Add someone
under Settings → Collaborators, or with
`gh api -X PUT repos/adamrothman/dustjacket/collaborators/<username>`;
removing them there ends their access.

## Pull requests from forks

The repository is public (decision 26), so anyone can fork it and open
issues and pull requests. A fork's pull request runs its own copy of
`test.yml`, with no secrets and a read-only `GITHUB_TOKEN`. Every run
for someone who isn't a collaborator waits for approval in the pull
request's checks (the repository's Actions settings require it for all
outside contributors, `all_external_contributors`, not only first-time
ones), so read its changes, `.github/` above all, before approving. Its
comments reach agent sessions that watch the pull request, like a
collaborator's.

## Security reports

`.github/SECURITY.md` asks people to report a vulnerability privately,
with **Report a vulnerability** on the Security and quality tab (private
vulnerability reporting is on). A report arrives as a proposed security
advisory there, and GitHub notifies Adam.

## The deploy role's ARN

The Deploy workflow reads the role it assumes from the `AWS_ROLE_ARN`
Actions secret, so the AWS account ID stays out of the repository and the
logs (decision 25). If the role is replaced, set the secret from the
stack's output:

```sh
gh secret set AWS_ROLE_ARN -R adamrothman/dustjacket --body "$(aws --profile apps iam get-role --role-name github-actions-dustjacket --query Role.Arn --output text)"
```

## Local development

```sh
go test ./...
DUSTJACKET_ADMIN_IDS=<your Hardcover user ID> go run ./cmd/dustjacket serve -memory
```

`-memory` needs no AWS. claude.ai cannot reach localhost, so the OAuth
flow cannot be finished by hand locally; `cmd/dustjacket`'s end-to-end
test walks the whole flow instead. The local server is for looking at the
pages.

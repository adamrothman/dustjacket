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
17. **The KMS key does not rotate automatically** (amends 5). It seals a
    handful of small secrets, once per connect, so key exhaustion is not a
    concern; rotation would not re-seal the keys already stored, since KMS
    keeps the old material to decrypt them; and each of the first two
    rotations adds $1 a month. Rotating or replacing the key is in the
    runbook. The organization's CloudTrail key, which protects the audit
    logs of every account, does rotate.
18. **Each sealed key records the KMS key that sealed it** (amends 5 and
    17), in the user record's `key_sealed_by`, and the app accepts previous
    keys for opening (`DUSTJACKET_KMS_PREVIOUS_KEY_IDS`). A key opened with
    a previous key is sealed again under the current one on its next use,
    unless the person reconnected in the meantime (a conditional write on
    `key_updated_at`). So the KMS key can be replaced without downtime or
    reconnecting, and `Decrypt` still always names its key, as AWS
    recommends. Key IDs are key ARNs, the form KMS reports; the app refuses
    to start with anything else.
19. **The allowlist is checked on every call and every token refresh, not
    only at connect** (amends 4). Taking a username off the list cuts off
    that person's existing connections at once: their calls get a 401 and
    their refreshes are refused, so Claude asks them to reconnect and the
    connect page turns them away. A grant whose user record is gone gets no
    more tokens either.
20. **A mutation that got no answer is not "safe to retry"** (amends the
    spec's error table). Dustjacket waits 20 seconds and Hardcover allows
    30, so a mutation can land after Claude is told it failed. For
    mutations, a timeout or 5xx says the change may or may not have been
    made and to check with `hardcover_query` first; a result over the size
    limit says the change was made and not to run it again. Reads keep
    "safe to retry".
21. **The renamed `WWW-Authenticate` header stays as it is.** Lambda function
    URLs rename it to `x-amzn-remapped-www-authenticate`, so a 401 from
    `/mcp` doesn't carry the protected-resource metadata URL in the standard
    header (pickem's does the same). The MCP authorization spec requires
    servers to offer one of the header or the well-known URIs, and Dustjacket
    serves both `/.well-known/oauth-protected-resource/mcp` and
    `/.well-known/oauth-protected-resource`; it requires clients to fall back
    to those when the header gives no location, which claude.ai does. A
    CloudFront function could restore the header, at the cost of another
    resource, for no client that needs it.
22. **The key can follow, like and block** (amends 11). `write:social` is
    in the scopes, so people can ask Claude to follow or unfollow someone,
    like something, block someone, or delete one of their own activities,
    such as the public one that marking an edition owned posts. Hardcover
    grants these together, with nothing narrower: its scope map puts
    follows, likes, blocks and deleting activities all under
    `write:social`. They are mutations, so they go through
    `hardcover_mutate`, which the runbook says to keep approving call by
    call. Keys made before this lack the scope: the refusal names it and
    links a new key, and reconnecting with one picks it up.
23. **Admins manage the allowlist from Claude, and it lives in the table**
    (amends 4 and 19). Adding someone used to take a Terraform change and an
    apply, which only Adam's laptop can do.
    - Admins are Hardcover user IDs in `DUSTJACKET_ADMIN_IDS`, set by
      Terraform; the app refuses to start with anything but numeric IDs.
      They are allowed by configuration alone and never on the list, so
      the list can't lock them out, and nobody can remove them from it.
      They are named by ID because a username can be given up and
      taken by someone else, which would hand that person admin.
    - The allowlist is one item (`ALLOWLIST`/`META`): the people allowed,
      keyed by Hardcover user ID with the username they had when added,
      and a version that every change is conditioned on, so two changes at
      once don't lose either. Checking it is one read, done on every call
      and refresh as decision 19 requires, and never cached, so a removal
      still cuts someone off at once. Matching IDs, not usernames, means a
      renamed account keeps working. `DUSTJACKET_ALLOWED_USERS` is gone.
      Since checking now reads the table, the token endpoint checks the
      grant before it uses up the code or the refresh token, and a read
      that fails answers `server_error` rather than `invalid_grant`: the
      client tries again with the same token, where before it would have
      made the person reconnect.
    - Admins get three more tools, which nobody else is given:
      `allowlist_list`; `allowlist_add`, which looks the username up on
      Hardcover with the admin's key (`users` by `username`, L21) and
      refuses a name it can't find; and `allowlist_remove`, which deletes
      the person's user record, sealed key included, in the same
      transaction, since nothing needs the key of someone removed. A
      connect conditions its write on the allowlist version it checked, so
      it can't store the key of someone removed meanwhile.
    - Claude reads other people's Hardcover text, which could ask for a
      change. The changes are marked as writes (`allowlist_remove` as
      destructive), so claude.ai asks before each one; their descriptions
      say to act only when the person names who; and each is in the
      request's log line (`target`, `target_id`, `result`). The worst a
      forged addition gives someone is the use of their own Hardcover
      account through Dustjacket.
24. **Only whoever started a Deploy run may re-run it into a deploy.** The
    deploy role trusts only runs on `main` whose `actor_id` is Adam's
    (`personal-infra`), but a re-run keeps the run's actor whoever starts
    it: GitHub records the person who clicked re-run only as
    `github.triggering_actor`, which isn't in the OIDC token, and AWS can't
    check the attempt number either. A collaborator, whose write access
    includes re-running workflows, could otherwise re-run one of Adam's
    deploys from the last 30 days and put that run's older code back on the
    function. (The agents' GitHub App can't: its Actions permission is
    read-only.) The deploy job runs only when `github.triggering_actor`
    is `github.actor`, so it's skipped on anyone else's re-run. A re-run
    uses the workflow file of the commit it repeats, and only Adam updates
    `main`, so nobody else can remove the check. Runs from before it lack
    it and stay re-runnable until GitHub's 30 days run out, unless deleted.
25. **The AWS account ID stays out of the repository and the logs.** It's
    no credential, but Adam keeps it private, and the repository is meant
    to be public. The Deploy workflow reads the role's ARN from the
    `AWS_ROLE_ARN` Actions secret rather than a variable: GitHub prints
    variables in logs as they are and masks secrets wherever they appear,
    the step's inputs included. `mask-aws-account-id` masks the bare ID
    too, which `configure-aws-credentials` doesn't by default and which AWS
    error messages carry. The original plan names the account as a
    placeholder. A secret is readable by a workflow on any branch in the
    repository, but this one grants nothing: the role still trusts only
    Adam's runs on `main`.
26. **The repository is public, and its history starts at this commit.**
    Dustjacket moved to a new public repository on 2026-10-07. Its first
    commit holds the tree the earlier repository's `main` had then, with
    no parent; the earlier history stays in a private repository because
    it holds data kept private. The rulesets came over unchanged, and the
    deploy role trusts the new repository's ID (`personal-infra`). Anyone
    can now read the code, the docs and the Actions logs, which decision
    25 keeps the account ID out of, and open issues and pull requests from
    forks: a fork's pull request runs the Test workflow with no secrets
    and a read-only token, and waits for approval unless its author is a
    collaborator (runbook, "Pull requests from forks"). (Amended: this
    first said only a first-time contributor's waits, GitHub's default;
    the repository now requires approval for every outside
    contributor's run.)

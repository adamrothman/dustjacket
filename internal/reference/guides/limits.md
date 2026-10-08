# Limits

- **Rate:** 60 requests a minute in bursts of at most 10 (15 for Hardcover
  supporters), and 5,000 a day (50,000 for supporters). Every top-level
  field in a request counts as one request, aliases included. Each GraphQL
  result carries a `rate_limit` value with what remains, e.g.
  `"Free";r=9;t=0, "daily";r=4999;t=64430`: `r` is requests left, `t`
  seconds until the window resets.
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

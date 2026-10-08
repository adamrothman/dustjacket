Run on 2026-09-23 against a well-known book not in the library, with a key
made from the connect page's scope link (and, for L5, a second key with
only `read:me:content`, `read:library` and `write:library`). The library
entry was private; everything the checks created was deleted afterwards,
except one public activity from L2 (see there).

| # | Question | Finding |
|---|---|---|
| L1 | Does `update_user_book` with only `status_id` keep the `rating` and `private_notes` it had, or null them? | Kept. Fields left out of the update keep their values (checked for `rating` and `private_notes`). |
| L1-read | Does `update_user_book_read` with only `progress_pages` keep `started_at`, or null it? | Kept: `started_at` and `edition_id` were unchanged and `progress_pages` was set. |
| L2 | Is `edition_owned` a toggle? | No. Two calls left the edition on the person's `owned` list (both returned the same `list_book` id). The library entry's `owned` stayed `false` throughout. It also posts a public `ListActivity` to the person's feed, which the key could not delete: deleting activities needs `write:social`, which keys lacked until decision 22 (see L20). |
| L3 | Does `user_books` without a `where` return only the caller's rows? | No: it returned other users' entries. Always filter on `user_id`. |
| L4 | Where are each match's fields in `search`'s `results`, and which fields does a book match carry? | `results.hits[].document`, whose `id` is a string (`ids` has the same ids as numbers). A book match carries `id`, `title`, `subtitle`, `author_names`, `release_year`, `series_names`, `series_ids`, `pages`, `rating`, `ratings_count`, `users_count`, `users_read_count`, `genres`, `moods`, `tags`, `content_warnings`, `isbns`, `has_audiobook`, `has_ebook`, `description` and more. Catalog stubs with no `author_names` and few `users_count` can outrank the book people mean. Five book matches came to about 52 KB, ten to about 84 KB. |
| L5 | Did the review save with the key lacking `write:reviews`? | No: HTTP 403 `insufficient_scope`, scope `write:reviews`. With the full key it saved (`has_review` true). `write:reviews` stays in the scopes. |
| L6 | Does `me { id username }` work with `read:me:content` but not `read:me`? | Yes. Other `users` fields can be invisible to a key even though the schema lists them: `me { default_reading_format_id }` failed with `field 'default_reading_format_id' not found in type: 'users'` (`validation-failed`). |
| L7 | Does `progress_seconds` persist without an audiobook edition? | No. Progress is kept in the unit of the read's edition: on a print edition `progress_pages` persisted and `progress_seconds` was ignored; on an audiobook edition `progress_seconds` persisted and `progress_pages` was ignored. |
| L8 | What does a journal entry with `privacy_setting_id: 0` return? | No error and an `id`, but `reading_journal: null`: the entry is created but cannot be read back. With 3 it was created normally. |
| L9 | Refusals and the `RateLimit` header | Bad key: HTTP 401 `{"error":"invalid_token","error_description":"..."}` with `WWW-Authenticate: Bearer realm="hardcover", error="invalid_token", ...`. Missing scope: HTTP 403 `{"error":"insufficient_scope","error_description":"Missing scopes: ...","scope":"..."}`. Rate limited: HTTP 429 `{"error":"Too Many Requests","message":"API rate limit exceeded for tier 'Free'. Try again in N seconds."}` with `Retry-After: N`. `RateLimit` looks like `"Free";r=9;t=0, "daily";r=4999;t=64430`; the burst is 10, and every top-level field, aliases included, spends one. Six aliased `me` fields in one request succeeded (HTTP 200): the documented 5-field cap was not enforced for them. A query naming a field the key cannot see returns HTTP 200 with `errors` (`validation-failed`). |
| L10 | What happens to reads when the status becomes 2 (Currently Reading)? | Hardcover creates a read itself, `started_at` today, with an edition chosen for the person (here the book's default audiobook edition). Status changes, ratings, progress and list additions also create journal entries (`status_want_to_read`, `rated`, `user_book_read_started`, `progress_updated`, `list_book`); the `list_book` ones were public even though the library entry was private. |

Run on 2026-09-24 (`TestLiveFinish`), each check on a fresh private entry
for the same book, removed afterwards with its journal entries; nothing
was left behind and no public activity was posted.

| # | Question | Finding |
|---|---|---|
| L11 | What does status 3 do to an open read? | Closes it: `finished_at` today, progress filled to the whole edition. No second read. `read_count` 1, `last_read_date` today. |
| L12 | What does adding a book with status 3 do? | Creates a finished read: `finished_at` today, no `started_at`. |
| L13 | What does setting `finished_at` on the open read do? | Sets the entry's status to 3 by itself (`last_read_date` follows the date given). Setting status 3 afterwards changes nothing. |
| L14 | Add a past read (started and finished) to an entry at status 1, then set status 3? | Adding the read leaves the status at 1 (`last_read_date` follows it). Setting status 3 then **rewrites that read's `finished_at` to today**. |
| L15 | Set status 3 first, then give the read it created past dates? | The dates stick; status stays 3; `last_read_date` follows. This is the order that works. |

Run on 2026-10-06 (`TestLiveSocial`, and `TestLiveWrites` with
`HARDCOVER_CHECK_OWNED=1`) with a key made from the connect page's link,
which now includes `write:social`. The follows and the like went to a
Hardcover account the key's owner didn't follow, the block to a stranger
with no follows either way. Each was undone, and `TestLiveWrites` left
nothing behind.

| # | Question | Finding |
|---|---|---|
| L16 | Follow and unfollow a user | `insert_followed_user(user_id:)` returned `error: null` and the `followed_users` row's `id`; a second call returned the same `id` and made no second row. Following posted no activity and made no `follows` row. `delete_followed_user(user_id:)` returned the same `id`; called again, with nobody to unfollow, `id: null` and `error: null`. |
| L17 | Follow an author | `insert_follow(followable_id: <author id>, followable_type: "Author")` returned `error: null` and the `follows` row's `id`; the row's `author` is the author. A second call returned the same `id`. `delete_follow` with the same arguments returned `success: true` and removed the row. `follows` with no `where` returned no rows while the key's owner followed nothing. |
| L18 | Like and unlike an activity | `upsert_like(likeable_id: <activity id>, likeable_type: "Activity")` returned the like's `id` and the activity's new `likes_count` (8 to 9). A second call returned the same `id` and left the count at 9: it is not a toggle. `delete_like` with the same arguments returned `likes_count` 8 and removed the like. |
| L19 | Block and unblock a user | `insert_block(blocked_user_id:)` returned `error: null` and the `user_blocks` row's `id`; a second call returned the same `id`. While blocked, the user's profile and activities could still be read. `delete_user_blocks(where: {user_id: {_eq: <me>}, blocked_user_id: {_eq: <them>}})` returned `affected_rows: 1` and unblocked. |
| L20 | Can the key delete the activity L2 posts? | Yes: `delete_activities(where: {id: {_eq: <id>}, user_id: {_eq: <me>}})` returned `affected_rows: 1`, and the activity was gone. The test book's private library entry had also posted a `UserBookActivity` with `privacy_setting_id` 3, deleted the same way. No private activities from earlier runs remained. |
| L1-read | (rerun) | `update_user_book_read` returned `{"error":"Request timeout"}`, and the read was unchanged right afterwards, but the change had landed two requests later. A write Hardcover reports as timed out can still happen (decision 20). |

Run on 2026-10-07 through Dustjacket's own `hardcover_query`, with an
admin's connected key, and since added to `TestLiveReads`.

| # | Question | Finding |
|---|---|---|
| L21 | Does `users(where: {username: {_eq: $username}})` find someone by username, and how? | Yes, with `$username: citext!`. It ignores case: the key owner's username in other capitals, and two followed users' in odd capitals, each returned the one user with the username as Hardcover stores it. It found a user the key's owner doesn't follow too. A username nobody has returned `users: []` and no error. Not checked: a private account, or one that has blocked the key's owner. |

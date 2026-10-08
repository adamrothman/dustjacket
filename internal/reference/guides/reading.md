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
    edition { id reading_format_id pages audio_seconds }
  }
}
```

## Start reading

Setting a library entry to status 2 (with `insert_user_book` or
`update_user_book`) makes Hardcover create a read by itself, started
today; use that one (see above) rather than adding another. Add a read
yourself only for a re-read, or to backdate a start:

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
Fix the start date with `update_user_book_read` and `started_at`.

Moving a book back from status 2 leaves the read Hardcover created. If the
person hadn't really started it, delete that read with
`delete_user_book_read`.

## Update progress

Send only what changes, e.g. `{progress_pages: 150}`.

```graphql
mutation Progress($id: Int!, $read: DatesReadInput!) {
  update_user_book_read(id: $id, object: $read) {
    error
    user_book_read { id started_at progress_pages progress_seconds }
  }
}
```

Progress is kept in the unit of the read's edition: `progress_pages` on a
print edition, `progress_seconds` on an audiobook edition; the other is
ignored without an error. The read Hardcover creates may be on an
audiobook edition, so to log pages, set `edition_id` to a print edition
(the book's `default_physical_edition { id }`) in the same update as
`progress_pages`. Check the open read's edition first: `reading_format_id`
1 is read, 2 listened, 4 ebook.

## Finish

Hardcover keeps a finished read and status 3 in step itself, so do one
thing, not both:

- **Finished today:** set the library entry to status 3 (with a rating, if
  the person gave one) with `update_user_book`. Hardcover closes the open
  read with today's date, or creates a finished read if there is none.
- **Finished another day, while reading it:** set `finished_at` on the
  open read with `update_user_book_read`. Hardcover sets status 3 itself.
- **Read in the past, not marked read:** set status 3 first
  (`insert_user_book` or `update_user_book`), which creates a read finished
  today, then set that read's `started_at` and `finished_at` to the real
  dates. Not the other way round: setting status 3 after adding a past
  read rewrites that read's `finished_at` to today.

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

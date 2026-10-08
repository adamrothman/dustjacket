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
  Before creating anything (a list, a goal, a journal entry), ask the
  person who should see it; if they don't say, use 3.

## Topics

- `library`: the person's books, statuses, ratings, reviews, owned editions
- `reading`: reads, progress, start and finish dates, reading history
- `search`: finding books, authors, series and users by name
- `catalog`: books, editions, authors, series
- `lists`: the person's lists
- `goals`: reading goals
- `journal`: notes and quotes
- `social`: following people and authors, what friends are reading,
  likes, blocks, deleting their own activities
- `recommendations`: suggesting books from their history
- `limits`: rate limits, request limits, disabled operators

## Traps

- Names match only through `search`; `_like` and `_ilike` are disabled.
- A request may have at most 5 top-level fields, or 1 `search` alone.
- Mutations that update take the row's own id (`user_books.id`,
  `user_book_reads.id`, `list_books.id`), not the book id.
- `user_books` without a `user_id` filter returns other people's entries
  too.
- Setting status 2 creates a read by itself; don't add a second one.
- Setting status 3 dates the latest read today, even over a date already
  there; for another date, set the status first, then the read's dates.
- Progress is kept in the unit of the read's edition (pages or seconds);
  see the reading guide.
- A journal entry needs `privacy_setting_id` 1, 2 or 3.
- Marking an edition owned posts a public activity; ask first (the
  social guide shows how to delete one).
- A "field ... not found" error for a field the schema lists means the
  person's key can't see that field.

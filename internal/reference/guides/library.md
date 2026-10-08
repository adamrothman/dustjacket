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

Send only the fields that change; the others keep their values.

```graphql
mutation Update($id: Int!, $object: UserBookUpdateInput!) {
  update_user_book(id: $id, object: $object) {
    error
    user_book { id status_id rating review_markdown private_notes }
  }
}
```

Setting status 3 also finishes the latest read, dated today; for any
other date, see the reading guide's "Finish".

## Remove a book

```graphql
mutation Remove($id: Int!) {
  delete_user_book(id: $id) { id }
}
```

## Owned editions

`edition_owned(id: <edition id>)` marks an edition as owned.

Calling it again leaves the edition owned. The edition appears on the
person's `owned` list; the library entry's `owned` field does not change.
It also posts a public activity to their feed, so ask before marking
anything owned; the social guide shows how to delete it.

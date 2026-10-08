# Catalog: books, editions, authors, series

A book is the work; editions are its printings, ebooks and audiobooks.
Library entries, lists and reads point at books, and optionally an edition.

## A book

```graphql
query Book($id: Int!) {
  books_by_pk(id: $id) {
    id
    title
    subtitle
    slug
    release_year
    pages
    rating
    ratings_count
    description
    cached_tags
    contributions { contribution author { id name } }
    book_series { position series { id name } }
    default_physical_edition { id pages isbn_13 }
    default_audio_edition { id audio_seconds }
  }
}
```

`cached_tags` holds the book's top genres, moods and tags.

## An edition by ISBN

```graphql
query ByIsbn($isbn: String!) {
  editions(where: {isbn_13: {_eq: $isbn}}) {
    id
    title
    edition_format
    pages
    book { id title }
  }
}
```

Use `isbn_10` for ten-digit ISBNs and `asin` for Kindle and Audible ids.

## An author's best-known books

```graphql
query Author($id: Int!) {
  authors_by_pk(id: $id) {
    name
    books_count
    contributions(
      where: {contributable_type: {_eq: "Book"}}
      order_by: {book: {users_count: desc}}
      limit: 20
    ) {
      contribution
      book { id title release_year }
    }
  }
}
```

## The books in a series, in order

The catalog has duplicate and partial books; this filter keeps one book per
position:

```graphql
query Series($id: Int!) {
  book_series(
    where: {series_id: {_eq: $id}, book: {canonical_id: {_is_null: true}, is_partial_book: {_eq: false}}}
    order_by: {position: asc}
    distinct_on: position
  ) {
    position
    book { id title release_year }
  }
}
```

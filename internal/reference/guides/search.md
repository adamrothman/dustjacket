# Search

Text matching works only through `search`: `_like`, `_ilike` and the regex
operators are disabled, so `where: {title: {_ilike: ...}}` fails. A request
may contain one `search` and nothing else at the top level, and search has a
2-second timeout.

`query_type` is one of `book` (the default), `author`, `series`, `list`,
`user`, `character`, `publisher`, `prompt` (any case). `per_page` defaults
to 25; ask for 5. A book match runs to about 10 KB, so 10 matches come
close to Dustjacket's 100 KB limit.

```graphql
query FindBook($q: String!) {
  search(query: $q, query_type: "book", per_page: 5) {
    ids
    results
  }
}
```

`ids` are the matches' ids in rank order. `results` is the search engine's
raw JSON.

Each match is in `results.hits[].document`, whose `id` is a string (`ids`
has the same ids as numbers). A book's carries `id`, `title`, `subtitle`,
`author_names`, `release_year`, `series_names`, `pages`, `rating`,
`users_count`, `genres`, `moods` and `tags`, often enough to answer
without a second request.

The top hit is not always the book the person means: catalog stubs with no
`author_names` and a handful of `users_count` can outrank it. Prefer the
match with authors and the most users, and confirm with the person when it
is unclear.

To get more than `results` carries, take `ids` and fetch those rows in a
second request. `_in` does not keep order, so re-sort by the position in
`ids`:

```graphql
query Hydrate($ids: [Int!]!) {
  books(where: {id: {_in: $ids}}) {
    id
    title
    release_year
    pages
    rating
    contributions { author { name } }
    book_series { position series { name } }
  }
}
```

Searching authors, series or users works the same way; hydrate with
`authors`, `series` or `users`.

Book search looks in `title`, `isbns`, `series_names`, `author_names` and
`alternative_titles` by default. To search other fields, pass `fields`
with one weight each, e.g. `fields: "genres", weights: "1"`.

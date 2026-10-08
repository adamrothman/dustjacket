# Reading journal

Journal entries are notes and quotes about a book, plus events Hardcover
records itself (`status_read`, `rated`, `progress_updated`, ...).

```graphql
query Journal($me: Int!) {
  reading_journals(where: {user_id: {_eq: $me}}, order_by: {action_at: desc}, limit: 20) {
    id
    event
    entry
    action_at
    privacy_setting_id
    book { id title }
  }
}
```

## Add a note or a quote

`event` is `note` or `quote`. `privacy_setting_id` and `tags` are
required. Ask who should see the entry (1 Public, 2 Followers,
3 Private); if the person doesn't say, use 3.

With `privacy_setting_id: 0` the entry is created but can't be read back
(no error; `reading_journal` comes back null); use 1, 2 or 3.

```graphql
mutation Note($book: Int!, $entry: String!, $privacy: Int!) {
  insert_reading_journal(object: {book_id: $book, event: "note", entry: $entry, privacy_setting_id: $privacy, tags: []}) {
    id
    errors
    reading_journal { id }
  }
}
```

`delete_reading_journal(id:)` removes an entry.

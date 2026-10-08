# Lists

Privacy IDs (`privacy_setting_id`): 1 Public, 2 Followers, 3 Private.

## My lists

```graphql
query MyLists($me: Int!) {
  lists(where: {user_id: {_eq: $me}}, order_by: {updated_at: desc}) {
    id
    name
    slug
    books_count
    privacy_setting_id
    ranked
  }
}
```

## A list's books

```graphql
query ListBooks($list: Int!) {
  list_books(where: {list_id: {_eq: $list}}, order_by: {position: asc}) {
    id
    position
    book { id title contributions { author { name } } }
  }
}
```

## Create a list

Ask who should see it (privacy 1 Public, 2 Followers, 3 Private); if the
person doesn't say, use 3.

```graphql
mutation NewList($name: String!, $description: String, $privacy: Int!) {
  insert_list(object: {name: $name, description: $description, privacy_setting_id: $privacy, ranked: false}) {
    id
    errors
    list { id slug }
  }
}
```

## Add and remove books

```graphql
mutation AddToList($list: Int!, $book: Int!) {
  insert_list_book(object: {list_id: $list, book_id: $book}) {
    id
    list_book { id position }
  }
}
```

`delete_list_book(id: ...)` takes the `list_books.id`, not the book id.

```graphql
mutation RemoveFromList($id: Int!) {
  delete_list_book(id: $id) { id }
}
```

`update_list(id:, object:)` renames or re-describes a list;
`delete_list(id:)` deletes it. Confirm with the person before deleting.

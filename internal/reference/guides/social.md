# Friends and activity

## Who I follow

```graphql
query Following($me: Int!) {
  followed_users(where: {user_id: {_eq: $me}}) {
    followed_user { id username name books_count }
  }
}
```

## What they have been reading

Activities are status changes, ratings and reviews. Pass the ids from above:

```graphql
query FriendsActivity($ids: [Int!]!) {
  activities(where: {user_id: {_in: $ids}}, order_by: {created_at: desc}, limit: 25) {
    id
    event
    created_at
    likes_count
    user { username }
    book { id title }
  }
}
```

## Someone's public profile

```graphql
query Profile($username: citext!) {
  users(where: {username: {_eq: $username}}) {
    id
    username
    name
    bio
    books_count
    followers_count
  }
}
```

## Follow and unfollow someone

Pass the `id` from their profile. Following someone already followed
changes nothing and returns the same `id`. Following posts nothing to the
person's feed.

```graphql
mutation Follow($id: Int!) {
  insert_followed_user(user_id: $id) { id error }
}
```

```graphql
mutation Unfollow($id: Int!) {
  delete_followed_user(user_id: $id) { id error }
}
```

Unfollowing someone not followed returns `id: null`.

## Follow an author

Pass the author's id (catalog guide). Following again changes nothing.

```graphql
mutation FollowAuthor($id: Int!) {
  insert_follow(followable_id: $id, followable_type: "Author") { id error }
}
```

```graphql
mutation UnfollowAuthor($id: Int!) {
  delete_follow(followable_id: $id, followable_type: "Author") { success error }
}
```

```graphql
query FollowedAuthors($me: bigint!) {
  follows(where: {user_id: {_eq: $me}, followable_type: {_eq: "Author"}}) {
    author { id name }
  }
}
```

## Like and unlike

`likeable_type` is `Activity`, `List`, `UserBook` or `ReadingJournal`,
and `likeable_id` that thing's id: for an activity, its `id` from the
activity query above. Liking twice keeps one like.

```graphql
mutation Like($id: Int!) {
  upsert_like(likeable_id: $id, likeable_type: "Activity") { id likes_count }
}
```

```graphql
mutation Unlike($id: Int!) {
  delete_like(likeable_id: $id, likeable_type: "Activity") { likes_count }
}
```

## Block and unblock

Confirm with the person before blocking anyone.

```graphql
mutation Block($id: Int!) {
  insert_block(blocked_user_id: $id) { id error }
}
```

```graphql
query Blocked($me: Int!) {
  user_blocks(where: {user_id: {_eq: $me}}) {
    blocked_user { id username }
  }
}
```

```graphql
mutation Unblock($me: Int!, $id: Int!) {
  delete_user_blocks(where: {user_id: {_eq: $me}, blocked_user_id: {_eq: $id}}) {
    affected_rows
  }
}
```

## Delete one of their activities

For example the public one that marking an edition owned posts. Find it,
confirm with the person, then delete it by its `id`, always together with
their own `user_id`:

```graphql
query MyActivities($me: Int!) {
  activities(where: {user_id: {_eq: $me}}, order_by: {created_at: desc}, limit: 10) {
    id
    event
    created_at
    privacy_setting_id
    book { title }
  }
}
```

```graphql
mutation DeleteActivity($me: Int!, $id: Int!) {
  delete_activities(where: {id: {_eq: $id}, user_id: {_eq: $me}}) {
    affected_rows
  }
}
```

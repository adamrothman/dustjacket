# Recommendations

Hardcover has no recommendation query. Build one from the person's history:

1. **Taste.** Their highest-rated reads, with the books' `cached_tags`
   (genres, moods) and `cached_similar_book_ids`; also what they did not
   finish (status 5) and what is already on their TBR (status 1).

   ```graphql
   query Taste($me: Int!) {
     user_books(
       where: {user_id: {_eq: $me}, status_id: {_eq: 3}, rating: {_gte: 4}}
       order_by: {rating: desc}
       limit: 30
     ) {
       rating
       book { id title cached_tags cached_similar_book_ids contributions { author { id name } } }
     }
   }
   ```

2. **Candidates.** Any of: similar books (`cached_similar_book_ids`, then
   hydrate with `books`), other books by loved authors (catalog guide),
   the next book in their series (catalog guide), what friends rated highly
   (social guide), or what is trending:

   ```graphql
   query Trending {
     books_trending(duration: month, limit: 20) {
       ids
       error
     }
   }
   ```

3. **Filter.** Drop anything already in their library:

   ```graphql
   query Known($me: Int!, $ids: [Int!]!) {
     user_books(where: {user_id: {_eq: $me}, book_id: {_in: $ids}}) {
       book_id
       status_id
     }
   }
   ```

4. **Explain.** Say why each pick fits, in terms of books they loved.

Each step is a request against the 60-a-minute limit; keep candidate lists
to a few dozen ids.

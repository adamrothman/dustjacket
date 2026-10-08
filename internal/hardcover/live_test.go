//go:build live

// Live checks against the real Hardcover API (docs/hardcover-notes.md).
// They log what Hardcover does, in lines starting with the check's
// number, and fail only when a request cannot be made at all. Run by
// hand, never in CI:
//
//	HARDCOVER_TOKEN=$(cat ~/.config/dustjacket/hardcover-token) \
//	HARDCOVER_TEST_BOOK_ID=<a book not in the library> \
//	go test -tags live -v -count=1 ./internal/hardcover/
//
// HARDCOVER_TOKEN_NO_REVIEWS, when set, is a second key with only
// read:me:content, read:library and write:library, for L5.
// HARDCOVER_SEARCH, when set, is what the search checks look for
// ("dune" otherwise): a way to find a test book's ID.
// HARDCOVER_CHECK_OWNED=1 also runs L2, which posts a public "added to
// Owned" activity, and L20, which deletes it.
//
// TestLiveReads also looks up HARDCOVER_TEST_USER by username, when set
// (L21).
//
// TestLiveSocial follows, likes and blocks people, and undoes each.
// HARDCOVER_TEST_USER is the username of someone the key's owner doesn't
// follow and who won't mind a follow and a like (L16, L18);
// HARDCOVER_TEST_BLOCK_USER, a stranger, with no follows either way, to
// block (L19). L17 follows the test book's first author.
//
// Requests are paced a second apart: Hardcover allows bursts of 10, spends
// one per top-level field, and refills about one a second. A 429 is waited
// out and the request sent again.
// The mutating checks add the test book to the library, privately, and
// remove it again; the book must not already be in the library.
package hardcover

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// liveURL is Hardcover's endpoint. The checks talk to it directly,
// without the client, so they can run before the client exists.
const liveURL = "https://api.hardcover.app/v1/graphql"

func liveEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s not set", name)
	}
	return v
}

// livePace is the pause before each request.
var livePace = time.Second

// livePost sends a GraphQL request straight to Hardcover and returns the
// status, the headers and the decoded body. It waits out a 429 and tries
// again, a few times, so a check never takes one for Hardcover's answer.
func livePost(t *testing.T, key, query string, vars map[string]any) (int, http.Header, map[string]any) {
	t.Helper()
	for range 3 {
		status, h, body := liveSend(t, key, query, vars)
		wait, err := strconv.Atoi(h.Get("Retry-After"))
		if status != http.StatusTooManyRequests || err != nil {
			return status, h, body
		}
		time.Sleep(time.Duration(wait+1) * time.Second)
	}
	return liveSend(t, key, query, vars)
}

// liveSend is livePost without the retries.
func liveSend(t *testing.T, key, query string, vars map[string]any) (int, http.Header, map[string]any) {
	t.Helper()
	time.Sleep(livePace)
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequest("POST", liveURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dustjacket-live-checks")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return res.StatusCode, res.Header, out
}

func show(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 1500 {
		return string(b[:1500]) + "…"
	}
	return string(b)
}

// dig follows keys and indexes (ints) into decoded JSON.
func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

// liveRows returns the rows at data.<field>, and stops the check when the
// response has none: a check must not act on a state it couldn't read.
func liveRows(t *testing.T, body map[string]any, field string) []any {
	t.Helper()
	rows, ok := dig(body, "data", field).([]any)
	if !ok {
		t.Fatalf("no %s in %s", field, show(body))
	}
	return rows
}

func liveMe(t *testing.T, key string) int {
	t.Helper()
	status, h, body := livePost(t, key, `query { me { id username } }`, nil)
	t.Logf("L6: me with the main key: HTTP %d %s", status, show(body))
	t.Logf("L9: RateLimit header: %q", h.Get("RateLimit"))
	id, ok := dig(body, "data", "me", 0, "id").(float64)
	if !ok {
		t.Fatalf("me failed: %s", show(body))
	}
	return int(id)
}

func TestLiveReads(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	me := liveMe(t, key)

	_, _, body := livePost(t, key, `query { user_books(limit: 5) { id user_id } }`, nil)
	t.Logf("L3: user_books with no where (me is %d): %s", me, show(body))

	q := os.Getenv("HARDCOVER_SEARCH")
	if q == "" {
		q = "dune"
	}
	for _, qt := range []string{"book", "author", "series", "user", "list"} {
		_, _, body := livePost(t, key, `query Search($q: String!, $t: String!) { search(query: $q, query_type: $t, per_page: 2) { ids results } }`, map[string]any{"q": q, "t": qt})
		t.Logf("L4: search %s: %s", qt, show(body))
		doc, _ := dig(body, "data", "search", "results", "hits", 0, "document").(map[string]any)
		t.Logf("L4: search %s document fields: %v", qt, slices.Sorted(maps.Keys(doc)))
	}

	// L21: looking someone up by username, as allowlist_add does: the key's
	// owner in capitals, a name nobody has, and HARDCOVER_TEST_USER if set.
	_, _, body = livePost(t, key, `query { me { username } }`, nil)
	name, _ := dig(body, "data", "me", 0, "username").(string)
	names := map[string]any{"upper": strings.ToUpper(name), "missing": "dustjacket-no-such-user-7f3a", "other": os.Getenv("HARDCOVER_TEST_USER")}
	_, _, body = livePost(t, key, `query Users($upper: citext!, $missing: citext!, $other: citext!) {
  upper: users(where: {username: {_eq: $upper}}) { id username }
  missing: users(where: {username: {_eq: $missing}}) { id username }
  other: users(where: {username: {_eq: $other}}) { id username }
}`, names)
	t.Logf("L21: users by username (me is %d, %q): %s", me, name, show(body))
}

func TestLiveErrors(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	status, h, body := livePost(t, "not-a-real-key", `query { me { id } }`, nil)
	t.Logf("L9: bad key: HTTP %d %s WWW-Authenticate=%q", status, show(body), h.Get("WWW-Authenticate"))
	status, h, body = livePost(t, key, `query { notification_deliveries(limit: 1) { id } }`, nil)
	t.Logf("L9: a scope the key lacks (read:notifications): HTTP %d %s", status, show(body))
	// Every top-level field spends one of the burst of 10, so let the
	// burst refill before asking for six at once.
	time.Sleep(10 * time.Second)
	status, h, body = livePost(t, key, `query { a: me { id } b: me { id } c: me { id } d: me { id } e: me { id } f: me { id } }`, nil)
	t.Logf("L9: six top-level fields: HTTP %d %s RateLimit=%q", status, show(body), h.Get("RateLimit"))
	// Then spend the burst, unpaced, to see a 429 and its headers.
	livePace = 0
	defer func() { livePace = time.Second }()
	for i := 0; i < 12 && status != http.StatusTooManyRequests; i++ {
		status, h, body = liveSend(t, key, `query { me { id } }`, nil)
	}
	t.Logf("L9: rate limited: HTTP %d %s Retry-After=%q RateLimit=%q", status, show(body), h.Get("Retry-After"), h.Get("RateLimit"))
}

func TestLiveWrites(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	book, err := strconv.Atoi(liveEnv(t, "HARDCOVER_TEST_BOOK_ID"))
	if err != nil {
		t.Fatal(err)
	}
	me := liveMe(t, key)

	_, _, body := livePost(t, key, `mutation Add($book: Int!) { insert_user_book(object: {book_id: $book, status_id: 1, rating: 4.5, private_notes: "dustjacket live test", privacy_setting_id: 3}) { id error user_book { id status_id rating private_notes } } }`, map[string]any{"book": book})
	t.Logf("setup: insert_user_book: %s", show(body))
	ubf, ok := dig(body, "data", "insert_user_book", "id").(float64)
	if !ok {
		t.Fatalf("could not add the test book: %s", show(body))
	}
	ub := int(ubf)
	// Cleanups run last-registered first: journal entries (Hardcover
	// writes some itself), then the library entry and its reads.
	t.Cleanup(func() {
		_, _, body := livePost(t, key, `mutation Remove($id: Int!) { delete_user_book(id: $id) { id } }`, map[string]any{"id": ub})
		t.Logf("cleanup: delete_user_book: %s", show(body))
		_, _, body = livePost(t, key, `query Left($me: Int!, $book: Int!) { user_books(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) { id } reading_journals(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) { id event } }`, map[string]any{"me": me, "book": book})
		t.Logf("cleanup: left behind: %s", show(body))
	})
	t.Cleanup(func() {
		_, _, body := livePost(t, key, `mutation Unjournal($book: Int!) { delete_reading_journals_for_book(book_id: $book) { ids } }`, map[string]any{"book": book})
		t.Logf("cleanup: delete_reading_journals_for_book: %s", show(body))
	})

	_, _, body = livePost(t, key, `mutation Partial($id: Int!) { update_user_book(id: $id, object: {status_id: 2}) { error user_book { status_id rating private_notes } } }`, map[string]any{"id": ub})
	t.Logf("L1: update_user_book with only status_id (rating was 4.5, private_notes set): %s", show(body))

	reads := func(when string) []any {
		_, _, body := livePost(t, key, `query Reads($ub: Int!) { user_book_reads(where: {user_book_id: {_eq: $ub}}) { id started_at finished_at progress_pages progress_seconds edition { id reading_format_id pages audio_seconds } } }`, map[string]any{"ub": ub})
		t.Logf("L10: reads %s: %s", when, show(body))
		r, _ := dig(body, "data", "user_book_reads").([]any)
		return r
	}
	var read int
	if r := reads("after setting status 2"); len(r) > 0 {
		read = int(dig(r, 0, "id").(float64))
	} else {
		_, _, body = livePost(t, key, `mutation Read($ub: Int!) { insert_user_book_read(user_book_id: $ub, user_book_read: {started_at: "2026-09-01"}) { id error } }`, map[string]any{"ub": ub})
		t.Logf("setup: insert_user_book_read: %s", show(body))
		rf, ok := dig(body, "data", "insert_user_book_read", "id").(float64)
		if !ok {
			t.Fatalf("no read to work with: %s", show(body))
		}
		read = int(rf)
	}

	_, _, body = livePost(t, key, `query Editions($book: Int!) { books_by_pk(id: $book) { default_physical_edition_id default_audio_edition_id } }`, map[string]any{"book": book})
	physical, _ := dig(body, "data", "books_by_pk", "default_physical_edition_id").(float64)
	t.Logf("L7: the book's default editions: %s", show(body))

	_, _, body = livePost(t, key, `mutation Print($id: Int!, $e: Int!) { update_user_book_read(id: $id, object: {started_at: "2026-09-01", edition_id: $e, progress_pages: 20}) { error user_book_read { started_at edition_id progress_pages progress_seconds } } }`, map[string]any{"id": read, "e": int(physical)})
	t.Logf("L7: set the print edition and progress_pages 20: %s", show(body))
	reads("after that")

	_, _, body = livePost(t, key, `mutation PartialRead($id: Int!) { update_user_book_read(id: $id, object: {progress_pages: 30}) { error user_book_read { started_at edition_id progress_pages } } }`, map[string]any{"id": read})
	t.Logf("L1-read: update_user_book_read with only progress_pages 30 (started_at was 2026-09-01, edition the print one): %s", show(body))
	reads("after that")

	_, _, body = livePost(t, key, `mutation Seconds($id: Int!, $e: Int!) { update_user_book_read(id: $id, object: {started_at: "2026-09-01", edition_id: $e, progress_seconds: 600}) { error user_book_read { edition_id progress_pages progress_seconds } } }`, map[string]any{"id": read, "e": int(physical)})
	t.Logf("L7: progress_seconds 600 on the print edition: %s", show(body))
	reads("after that")

	if os.Getenv("HARDCOVER_CHECK_OWNED") == "1" && physical != 0 {
		owned := func(when string) {
			_, _, body := livePost(t, key, `query Owned($me: Int!, $book: Int!, $e: Int!) { lists(where: {user_id: {_eq: $me}, slug: {_eq: "owned"}}) { list_books(where: {edition_id: {_eq: $e}}) { id } } user_books(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) { owned owned_copies } }`, map[string]any{"me": me, "book": book, "e": int(physical)})
			t.Logf("L2: owned %s: %s", when, show(body))
		}
		owned("before")
		since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		for i := 1; i <= 2; i++ {
			_, _, body := livePost(t, key, `mutation Own($e: Int!) { edition_owned(id: $e) { id list_book { id } } }`, map[string]any{"e": int(physical)})
			t.Logf("L2: edition_owned call %d: %s", i, show(body))
			owned("after call " + strconv.Itoa(i))
		}

		posted := func(when string) []any {
			_, _, body := livePost(t, key, `query Posted($me: Int!, $book: Int!, $since: timestamptz!) { activities(where: {user_id: {_eq: $me}, book_id: {_eq: $book}, created_at: {_gte: $since}}) { id event privacy_setting_id created_at } }`, map[string]any{"me": me, "book": book, "since": since})
			t.Logf("L20: the test book's activities %s: %s", when, show(body))
			return liveRows(t, body, "activities")
		}
		for _, a := range posted("after L2") {
			_, _, body := livePost(t, key, `mutation Unpost($me: Int!, $id: Int!) { delete_activities(where: {id: {_eq: $id}, user_id: {_eq: $me}}) { affected_rows } }`, map[string]any{"me": me, "id": int(dig(a, "id").(float64))})
			t.Logf("L20: delete_activities: %s", show(body))
		}
		posted("after deleting them")
	}

	_, _, body = livePost(t, key, `mutation Review($id: Int!) { update_user_book(id: $id, object: {status_id: 3, review_markdown: "dustjacket live test review"}) { error user_book { has_review review_markdown privacy_setting_id } } }`, map[string]any{"id": ub})
	t.Logf("L5: review with the main key: %s", show(body))
	if nr := os.Getenv("HARDCOVER_TOKEN_NO_REVIEWS"); nr != "" {
		status, _, body := livePost(t, nr, `mutation Review($id: Int!) { update_user_book(id: $id, object: {status_id: 3, review_markdown: "dustjacket live test review 2"}) { error user_book { has_review review_markdown } } }`, map[string]any{"id": ub})
		t.Logf("L5: review with a key lacking write:reviews: HTTP %d %s", status, show(body))
	} else {
		t.Logf("L5: HARDCOVER_TOKEN_NO_REVIEWS not set; skipped the second key")
	}

	for _, p := range []int{0, 3} {
		_, _, body := livePost(t, key, `mutation Journal($book: Int!, $p: Int!) { insert_reading_journal(object: {book_id: $book, event: "note", entry: "dustjacket live test", privacy_setting_id: $p, tags: []}) { id errors reading_journal { id privacy_setting_id } } }`, map[string]any{"book": book, "p": p})
		t.Logf("L8: journal note with privacy_setting_id %d: %s", p, show(body))
	}
}

// liveAdd puts the test book in the library, privately, with the status
// given, and removes it (and the journal entries Hardcover writes) when
// the test ends.
func liveAdd(t *testing.T, key string, me, book, status int) int {
	t.Helper()
	_, _, body := livePost(t, key, `mutation Add($book: Int!, $s: Int!) { insert_user_book(object: {book_id: $book, status_id: $s, privacy_setting_id: 3}) { id error } }`, map[string]any{"book": book, "s": status})
	id, ok := dig(body, "data", "insert_user_book", "id").(float64)
	if !ok {
		t.Fatalf("could not add the test book: %s", show(body))
	}
	t.Cleanup(func() {
		_, _, body := livePost(t, key, `mutation Remove($id: Int!) { delete_user_book(id: $id) { id } }`, map[string]any{"id": int(id)})
		t.Logf("cleanup: delete_user_book: %s", show(body))
		_, _, body = livePost(t, key, `query Left($me: Int!, $book: Int!) { user_books(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) { id } reading_journals(where: {user_id: {_eq: $me}, book_id: {_eq: $book}}) { id } }`, map[string]any{"me": me, "book": book})
		t.Logf("cleanup: left behind: %s", show(body))
	})
	t.Cleanup(func() {
		_, _, body := livePost(t, key, `mutation Unjournal($book: Int!) { delete_reading_journals_for_book(book_id: $book) { ids } }`, map[string]any{"book": book})
		t.Logf("cleanup: delete_reading_journals_for_book: %s", show(body))
	})
	return int(id)
}

// liveReads logs a library entry's reads and returns them.
func liveReads(t *testing.T, key string, ub int, label string) []any {
	t.Helper()
	_, _, body := livePost(t, key, `query Reads($ub: Int!) { user_book_reads(where: {user_book_id: {_eq: $ub}}, order_by: {id: asc}) { id started_at finished_at progress_pages progress_seconds edition_id } user_books_by_pk(id: $ub) { status_id last_read_date read_count } }`, map[string]any{"ub": ub})
	t.Logf("%s: %s", label, show(body))
	r, _ := dig(body, "data", "user_book_reads").([]any)
	return r
}

func liveStatus(t *testing.T, key string, ub, status int) {
	t.Helper()
	_, _, body := livePost(t, key, `mutation Status($id: Int!, $s: Int!) { update_user_book(id: $id, object: {status_id: $s}) { error } }`, map[string]any{"id": ub, "s": status})
	if dig(body, "data", "update_user_book", "error") != nil {
		t.Logf("status %d: %s", status, show(body))
	}
}

// What finishing a book does to its reads (L11–L15).
func TestLiveFinish(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	book, err := strconv.Atoi(liveEnv(t, "HARDCOVER_TEST_BOOK_ID"))
	if err != nil {
		t.Fatal(err)
	}
	me := liveMe(t, key)

	t.Run("L12", func(t *testing.T) {
		ub := liveAdd(t, key, me, book, 3)
		liveReads(t, key, ub, "L12: reads after adding the book with status 3")
	})
	t.Run("L11", func(t *testing.T) {
		ub := liveAdd(t, key, me, book, 1)
		liveStatus(t, key, ub, 2)
		liveReads(t, key, ub, "L11: reads after status 2")
		liveStatus(t, key, ub, 3)
		liveReads(t, key, ub, "L11: reads after status 3 with that read open")
	})
	t.Run("L13", func(t *testing.T) {
		ub := liveAdd(t, key, me, book, 1)
		liveStatus(t, key, ub, 2)
		r := liveReads(t, key, ub, "L13: reads after status 2")
		if len(r) == 0 {
			t.Fatal("no read to finish")
		}
		_, _, body := livePost(t, key, `mutation Finish($id: Int!) { update_user_book_read(id: $id, object: {finished_at: "2026-09-20"}) { error user_book_read { started_at finished_at } } }`, map[string]any{"id": int(dig(r, 0, "id").(float64))})
		t.Logf("L13: set finished_at 2026-09-20 on the open read: %s", show(body))
		liveReads(t, key, ub, "L13: reads after that")
		liveStatus(t, key, ub, 3)
		liveReads(t, key, ub, "L13: reads after status 3")
	})
	t.Run("L15", func(t *testing.T) {
		ub := liveAdd(t, key, me, book, 3)
		r := liveReads(t, key, ub, "L15: reads after adding the book with status 3")
		if len(r) == 0 {
			t.Fatal("no read to backdate")
		}
		_, _, body := livePost(t, key, `mutation Backdate($id: Int!) { update_user_book_read(id: $id, object: {started_at: "2026-09-01", finished_at: "2026-09-15"}) { error } }`, map[string]any{"id": int(dig(r, 0, "id").(float64))})
		t.Logf("L15: set that read to 2026-09-01..2026-09-15: %s", show(body))
		liveReads(t, key, ub, "L15: reads after that")
	})
	t.Run("L14", func(t *testing.T) {
		ub := liveAdd(t, key, me, book, 1)
		_, _, body := livePost(t, key, `mutation Past($ub: Int!) { insert_user_book_read(user_book_id: $ub, user_book_read: {started_at: "2026-09-01", finished_at: "2026-09-15"}) { id error } }`, map[string]any{"ub": ub})
		t.Logf("L14: add a read started 2026-09-01, finished 2026-09-15: %s", show(body))
		liveReads(t, key, ub, "L14: reads after that")
		liveStatus(t, key, ub, 3)
		liveReads(t, key, ub, "L14: reads after status 3")
	})
}

// liveUser looks a username up and returns the user's id.
func liveUser(t *testing.T, key, username string) int {
	t.Helper()
	_, _, body := livePost(t, key, `query User($u: citext!) { users(where: {username: {_eq: $u}}) { id } }`, map[string]any{"u": username})
	id, ok := dig(body, "data", "users", 0, "id").(float64)
	if !ok {
		t.Fatalf("no user %s: %s", username, show(body))
	}
	return int(id)
}

// Following, liking and blocking (L16–L19), each undone before the test
// ends.
func TestLiveSocial(t *testing.T) {
	key := liveEnv(t, "HARDCOVER_TOKEN")
	me := liveMe(t, key)

	t.Run("L16", func(t *testing.T) {
		friend := liveUser(t, key, liveEnv(t, "HARDCOVER_TEST_USER"))
		following := func(when string) bool {
			_, _, body := livePost(t, key, `query Following($me: Int!, $u: Int!, $me64: bigint!, $u64: bigint!) { followed_users(where: {user_id: {_eq: $me}, followed_user_id: {_eq: $u}}) { id created_at } follows(where: {user_id: {_eq: $me64}, followable_id: {_eq: $u64}}) { id followable_type } activities(where: {user_id: {_eq: $me}}, order_by: {id: desc}, limit: 1) { id event created_at } }`, map[string]any{"me": me, "u": friend, "me64": me, "u64": friend})
			t.Logf("L16: %s: %s", when, show(body))
			return len(liveRows(t, body, "followed_users")) > 0
		}
		unfollow := func(label string) {
			_, _, body := livePost(t, key, `mutation Unfollow($u: Int!) { delete_followed_user(user_id: $u) { id error } }`, map[string]any{"u": friend})
			t.Logf("L16: %s: %s", label, show(body))
		}
		if following("before") {
			t.Fatal("HARDCOVER_TEST_USER must be someone the key's owner doesn't follow")
		}
		t.Cleanup(func() {
			if following("cleanup") {
				unfollow("cleanup: delete_followed_user")
			}
		})
		for i := 1; i <= 2; i++ {
			_, _, body := livePost(t, key, `mutation Follow($u: Int!) { insert_followed_user(user_id: $u) { id error user_id followed_user_id } }`, map[string]any{"u": friend})
			t.Logf("L16: insert_followed_user call %d: %s", i, show(body))
			following("after follow " + strconv.Itoa(i))
		}
		for i := 1; i <= 2; i++ {
			unfollow("delete_followed_user call " + strconv.Itoa(i))
			following("after unfollow " + strconv.Itoa(i))
		}
	})

	t.Run("L17", func(t *testing.T) {
		book, err := strconv.Atoi(liveEnv(t, "HARDCOVER_TEST_BOOK_ID"))
		if err != nil {
			t.Fatal(err)
		}
		_, _, body := livePost(t, key, `query Types { follows(distinct_on: followable_type, limit: 20) { followable_type } }`, nil)
		t.Logf("L17: followable types in use: %s", show(body))
		_, _, body = livePost(t, key, `query Author($book: Int!) { books_by_pk(id: $book) { contributions(limit: 1) { author { id name } } } }`, map[string]any{"book": book})
		af, ok := dig(body, "data", "books_by_pk", "contributions", 0, "author", "id").(float64)
		if !ok {
			t.Fatalf("the test book has no author: %s", show(body))
		}
		author := int(af)
		following := func(when string) bool {
			_, _, body := livePost(t, key, `query Following($me: bigint!, $a: bigint!) { follows(where: {user_id: {_eq: $me}, followable_id: {_eq: $a}, followable_type: {_eq: "Author"}}) { id created_at author { id name } } }`, map[string]any{"me": me, "a": author})
			t.Logf("L17: %s: %s", when, show(body))
			return len(liveRows(t, body, "follows")) > 0
		}
		unfollow := func(label string) {
			_, _, body := livePost(t, key, `mutation Unfollow($a: Int!) { delete_follow(followable_id: $a, followable_type: "Author") { success error } }`, map[string]any{"a": author})
			t.Logf("L17: %s: %s", label, show(body))
		}
		if following("before") {
			t.Skip("the key's owner already follows the test book's author")
		}
		t.Cleanup(func() {
			if following("cleanup") {
				unfollow("cleanup: delete_follow")
			}
		})
		for i := 1; i <= 2; i++ {
			_, _, body := livePost(t, key, `mutation Follow($a: Int!) { insert_follow(followable_id: $a, followable_type: "Author") { id error followable_id followable_type } }`, map[string]any{"a": author})
			t.Logf("L17: insert_follow Author call %d: %s", i, show(body))
			following("after follow " + strconv.Itoa(i))
		}
		unfollow("delete_follow")
		following("after unfollow")
	})

	t.Run("L18", func(t *testing.T) {
		friend := liveUser(t, key, liveEnv(t, "HARDCOVER_TEST_USER"))
		_, _, body := livePost(t, key, `query Latest($u: Int!) { activities(where: {user_id: {_eq: $u}}, order_by: {id: desc}, limit: 1) { id event likes_count } }`, map[string]any{"u": friend})
		latest := liveRows(t, body, "activities")
		if len(latest) == 0 {
			t.Skip("HARDCOVER_TEST_USER has no activity to like")
		}
		activity := int(dig(latest, 0, "id").(float64))
		liked := func(when string) bool {
			_, _, body := livePost(t, key, `query Liked($me: Int!, $a: Int!) { likes(where: {user_id: {_eq: $me}, likeable_id: {_eq: $a}, likeable_type: {_eq: "Activity"}}) { id created_at } activities_by_pk(id: $a) { likes_count } }`, map[string]any{"me": me, "a": activity})
			t.Logf("L18: %s: %s", when, show(body))
			return len(liveRows(t, body, "likes")) > 0
		}
		unlike := func(label string) {
			_, _, body := livePost(t, key, `mutation Unlike($a: Int!) { delete_like(likeable_id: $a, likeable_type: "Activity") { likes_count } }`, map[string]any{"a": activity})
			t.Logf("L18: %s: %s", label, show(body))
		}
		if liked("before") {
			t.Skip("the key's owner already likes HARDCOVER_TEST_USER's latest activity")
		}
		t.Cleanup(func() {
			if liked("cleanup") {
				unlike("cleanup: delete_like")
			}
		})
		for i := 1; i <= 2; i++ {
			_, _, body := livePost(t, key, `mutation Like($a: Int!) { upsert_like(likeable_id: $a, likeable_type: "Activity") { id likes_count like { id likeable_type } } }`, map[string]any{"a": activity})
			t.Logf("L18: upsert_like call %d: %s", i, show(body))
			liked("after like " + strconv.Itoa(i))
		}
		unlike("delete_like")
		liked("after unlike")
	})

	t.Run("L19", func(t *testing.T) {
		stranger := liveUser(t, key, liveEnv(t, "HARDCOVER_TEST_BLOCK_USER"))
		vars := map[string]any{"me": me, "u": stranger}
		ties := func(when string) (follows, blocked bool) {
			_, _, body := livePost(t, key, `query Ties($me: Int!, $u: Int!) { followed_users(where: {_or: [{user_id: {_eq: $me}, followed_user_id: {_eq: $u}}, {user_id: {_eq: $u}, followed_user_id: {_eq: $me}}]}) { id user_id } user_blocks(where: {user_id: {_eq: $me}, blocked_user_id: {_eq: $u}}) { id created_at } users(where: {id: {_eq: $u}}) { id username } activities(where: {user_id: {_eq: $u}}, limit: 1) { id } }`, vars)
			t.Logf("L19: %s: %s", when, show(body))
			return len(liveRows(t, body, "followed_users")) > 0, len(liveRows(t, body, "user_blocks")) > 0
		}
		unblock := func(label string) {
			_, _, body := livePost(t, key, `mutation Unblock($me: Int!, $u: Int!) { delete_user_blocks(where: {user_id: {_eq: $me}, blocked_user_id: {_eq: $u}}) { affected_rows } }`, vars)
			t.Logf("L19: %s: %s", label, show(body))
		}
		if f, b := ties("before"); f || b {
			t.Fatal("HARDCOVER_TEST_BLOCK_USER must be a stranger: no follows either way, not blocked")
		}
		t.Cleanup(func() {
			if _, b := ties("cleanup"); b {
				unblock("cleanup: delete_user_blocks")
			}
		})
		for i := 1; i <= 2; i++ {
			_, _, body := livePost(t, key, `mutation Block($u: Int!) { insert_block(blocked_user_id: $u) { id error user_block { id } } }`, map[string]any{"u": stranger})
			t.Logf("L19: insert_block call %d: %s", i, show(body))
			ties("after block " + strconv.Itoa(i))
		}
		unblock("delete_user_blocks")
		ties("after unblock")
	})
}

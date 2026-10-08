package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

// connect registers a client, parses its authorize request and connects
// the given user through it, returning the client ID and the code.
func connect(t *testing.T, s *Server, userID, username, key string) (string, string) {
	t.Helper()
	_, out := register(t, s, callback)
	id := out["client_id"].(string)
	req, err := parse(s, authorizeQuery(id))
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.Connect(t.Context(), req, userID, username, key)
	if err != nil {
		t.Fatal(err)
	}
	return id, code
}

func postToken(s *Server, form url.Values) (int, map[string]any) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Token(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func exchange(s *Server, clientID, code, v string) (int, map[string]any) {
	return postToken(s, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {v}, "redirect_uri": {callback}})
}

func authenticate(s *Server, access string) (*Caller, error) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	return s.Authenticate(r.Context(), r)
}

func TestConnectStoresSealedKeyAndMakesClientPermanent(t *testing.T) {
	s, st, _ := newServer()
	clientID, _ := connect(t, s, "7", "adam", "hc-key-1")
	var u User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &u); err != nil {
		t.Fatal(err)
	}
	if u.Username != "adam" || len(u.KeyCiphertext) == 0 || strings.Contains(string(u.KeyCiphertext), "hc-key-1") || !strings.HasPrefix(u.KeySealedBy, "local:") {
		t.Fatalf("user: %+v", u)
	}
	var c Client
	if err := st.Get(t.Context(), store.Key{PK: "OAUTHCLIENT#" + clientID, SK: "META"}, &c); err != nil || c.Expires != nil {
		t.Fatalf("client after connect: %+v %v", c, err)
	}
}

func TestCodeExchangeRefreshRevoke(t *testing.T) {
	s, _, clk := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")

	if status, out := exchange(s, clientID, code, "wrong-verifier-wrong-verifier-wrong-verifier-x"); status != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %d %v", status, out)
	}
	// The failed attempt used the code up.
	if status, _ := exchange(s, clientID, code, verifier); status != 400 {
		t.Fatalf("code reused: %d", status)
	}

	clientID2, code := connect(t, s, "7", "adam", "hc-key-1")
	status, tok := exchange(s, clientID2, code, verifier)
	if status != 200 || tok["token_type"] != "Bearer" || tok["scope"] != "hardcover" {
		t.Fatalf("exchange: %d %v", status, tok)
	}
	caller, err := authenticate(s, tok["access_token"].(string))
	if err != nil || caller.UserID != "7" || caller.Username != "adam" || caller.Key != "hc-key-1" {
		t.Fatalf("authenticate: %+v %v", caller, err)
	}

	// Refresh rotates: the new pair works, the old refresh token doesn't.
	status, tok2 := postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID2}, "refresh_token": {tok["refresh_token"].(string)}})
	if status != 200 {
		t.Fatalf("refresh: %d %v", status, tok2)
	}
	if status, _ := postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID2}, "refresh_token": {tok["refresh_token"].(string)}}); status != 400 {
		t.Fatalf("old refresh token reused: %d", status)
	}

	// A connected client does not expire.
	clk.t = clk.t.Add(48 * time.Hour)
	status, tok3 := postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID2}, "refresh_token": {tok2["refresh_token"].(string)}})
	if status != 200 {
		t.Fatalf("refresh two days later: %d %v", status, tok3)
	}
	// Access tokens do.
	if _, err := authenticate(s, tok2["access_token"].(string)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired access token: %v", err)
	}

	// Revoking any token of the grant revokes the grant.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/revoke", strings.NewReader(url.Values{"token": {tok3["refresh_token"].(string)}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Revoke(w, r)
	if w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if _, err := authenticate(s, tok3["access_token"].(string)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestReconnectReplacesTheKeyForEveryGrant(t *testing.T) {
	s, st, clk := newServer()
	clientID, code := connect(t, s, "7", "adam", "old-key")
	_, tok := exchange(s, clientID, code, verifier)
	var before User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &before); err != nil {
		t.Fatal(err)
	}

	clk.t = clk.t.Add(time.Minute)
	connect(t, s, "7", "adam", "new-key") // through another client

	caller, err := authenticate(s, tok["access_token"].(string))
	if err != nil || caller.Key != "new-key" {
		t.Fatalf("first grant after reconnect: %+v %v", caller, err)
	}
	var after User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &after); err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) || !after.KeyUpdatedAt.Equal(clk.t) {
		t.Fatalf("created %v→%v, key updated %v", before.CreatedAt, after.CreatedAt, after.KeyUpdatedAt)
	}
}

func TestAuthenticateFailures(t *testing.T) {
	s, st, _ := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")
	_, tok := exchange(s, clientID, code, verifier)
	access := tok["access_token"].(string)

	for name, header := range map[string]string{"none": "", "basic": "Basic abc", "unknown": "Bearer nope", "refresh as access": "Bearer " + tok["refresh_token"].(string)} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if _, err := s.Authenticate(r.Context(), r); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Deleting the user record cuts off every connection they have.
	if err := st.Delete(t.Context(), store.Key{PK: "USER#7", SK: "META"}); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(s, access); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted user: %v", err)
	}
}

func TestChallenge(t *testing.T) {
	s, _, _ := newServer()
	w := httptest.NewRecorder()
	s.Challenge(w)
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != `Bearer resource_metadata="https://dustjacket.test/.well-known/oauth-protected-resource"` {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

// rotating is a sealer whose key can be replaced, as KMS's is with
// Previous: it seals with the newest key and opens with any of them.
type rotating struct {
	current  *sealer.Local
	previous []*sealer.Local
}

func (r *rotating) Seal(ctx context.Context, userID string, plaintext []byte) (sealer.Sealed, error) {
	return r.current.Seal(ctx, userID, plaintext)
}

func (r *rotating) Open(ctx context.Context, userID string, s sealer.Sealed) ([]byte, bool, error) {
	if pt, _, err := r.current.Open(ctx, userID, s); err == nil {
		return pt, false, nil
	}
	for _, p := range r.previous {
		if pt, _, err := p.Open(ctx, userID, s); err == nil {
			return pt, true, nil
		}
	}
	return nil, false, sealer.ErrOpen
}

func (r *rotating) rotate() {
	r.previous = append(r.previous, r.current)
	r.current = sealer.NewLocal()
}

func sealedBy(t *testing.T, st store.Store, userID string) User {
	t.Helper()
	var u User
	if err := st.Get(t.Context(), store.Key{PK: "USER#" + userID, SK: "META"}, &u); err != nil {
		t.Fatal(err)
	}
	return u
}

// After the key is replaced, a person's stored key still opens, and is
// sealed again under the new key the first time it is used.
func TestAuthenticateResealsUnderTheCurrentKey(t *testing.T) {
	s, st, _ := newServer()
	rot := &rotating{current: sealer.NewLocal()}
	s.Sealer = rot
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")
	_, tok := exchange(s, clientID, code, verifier)
	oldKey := sealedBy(t, st, "7").KeySealedBy

	rot.rotate()
	caller, err := authenticate(s, tok["access_token"].(string))
	if err != nil || caller.Key != "hc-key-1" {
		t.Fatalf("after rotation: %+v %v", caller, err)
	}
	u := sealedBy(t, st, "7")
	if u.KeySealedBy == oldKey || u.KeySealedBy == "" {
		t.Fatalf("still sealed by the old key: %q", u.KeySealedBy)
	}
	// With the old key gone entirely, it still opens.
	rot.previous = nil
	if caller, err := authenticate(s, tok["access_token"].(string)); err != nil || caller.Key != "hc-key-1" {
		t.Fatalf("after retiring the old key: %+v %v", caller, err)
	}
}

// A re-seal that races a reconnect must not put the old Hardcover key
// back over the new one.
func TestResealLosesToAReconnect(t *testing.T) {
	s, st, clk := newServer()
	rot := &rotating{current: sealer.NewLocal()}
	s.Sealer = rot
	connect(t, s, "7", "adam", "old-hc-key")
	before := sealedBy(t, st, "7") // what a request read, just before...
	rot.rotate()
	clk.t = clk.t.Add(time.Minute)
	connect(t, s, "7", "adam", "new-hc-key") // ...the person reconnected

	if err := s.reseal(t.Context(), &before, []byte("old-hc-key")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reseal over a reconnect: %v", err)
	}
	after := sealedBy(t, st, "7")
	key, _, err := rot.Open(t.Context(), "7", sealer.Sealed{Ciphertext: after.KeyCiphertext, KeyID: after.KeySealedBy})
	if err != nil || string(key) != "new-hc-key" {
		t.Fatalf("stored key: %q %v", key, err)
	}
}

func refresh(s *Server, clientID, token string) (int, map[string]any) {
	return postToken(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {token}})
}

// Taking someone off the allowlist cuts off the connections they already
// have, not just new ones, and deletes their stored key.
func TestDisallowCutsOffAndDeletesTheKey(t *testing.T) {
	s, st, _ := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")
	_, tok := exchange(s, clientID, code, verifier)
	if c, err := authenticate(s, tok["access_token"].(string)); err != nil || c.Admin {
		t.Fatalf("before: %+v %v", c, err)
	}

	removed, err := s.Disallow(t.Context(), " @ADAM ")
	if err != nil || removed == nil || removed.ID != "7" {
		t.Fatalf("disallow: %+v %v", removed, err)
	}
	if _, err := authenticate(s, tok["access_token"].(string)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("access token after removal: %v", err)
	}
	if status, out := refresh(s, clientID, tok["refresh_token"].(string)); status != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("refresh after removal: %d %v", status, out)
	}
	var u User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &u); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("user record after removal: %v", err)
	}
	if removed, err := s.Disallow(t.Context(), "adam"); err != nil || removed != nil {
		t.Fatalf("second removal: %+v %v", removed, err)
	}

	// Added back, they connect again with a new key; the old tokens stay dead.
	if added, err := s.Allow(t.Context(), AllowedUser{ID: "7", Username: "adam"}); !added || err != nil {
		t.Fatalf("allow: %v %v", added, err)
	}
	if _, err := authenticate(s, tok["access_token"].(string)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token after re-adding: %v", err)
	}
	connect(t, s, "7", "adam", "hc-key-2")
}

func TestAllowlist(t *testing.T) {
	s, _, clk := newServer()
	if added, err := s.Allow(t.Context(), AllowedUser{ID: "7", Username: "adam"}); added || err != nil {
		t.Fatalf("adding someone already there: %v %v", added, err)
	}
	if _, err := s.Allow(t.Context(), AllowedUser{ID: "1", Username: "boss"}); !errors.Is(err, ErrAdmin) {
		t.Fatalf("adding an admin: %v", err)
	}
	if added, err := s.Allow(t.Context(), AllowedUser{ID: "8", Username: "Sister", AddedBy: "boss"}); !added || err != nil {
		t.Fatalf("allow: %v %v", added, err)
	}
	l, err := s.Allowlist(t.Context())
	if err != nil || l.Version != 2 || len(l.Users) != 2 || l.Users["8"] != (AllowedUser{ID: "8", Username: "Sister", AddedAt: clk.t, AddedBy: "boss"}) {
		t.Fatalf("list: %+v %v", l, err)
	}

	// A list nobody has written yet is empty, and the first write makes it.
	s.Store = store.NewMemory()
	if l, err := s.Allowlist(t.Context()); err != nil || l.Version != 0 || len(l.Users) != 0 {
		t.Fatalf("empty list: %+v %v", l, err)
	}
	if added, err := s.Allow(t.Context(), AllowedUser{ID: "8", Username: "sister"}); !added || err != nil {
		t.Fatalf("first allow: %v %v", added, err)
	}
	if l, _ := s.Allowlist(t.Context()); l.Version != 1 {
		t.Fatalf("first version: %d", l.Version)
	}
}

// An admin connects without being on the allowlist; anyone else not on
// it is refused before their key is sealed or stored.
func TestConnectWhoIsAllowed(t *testing.T) {
	s, st, _ := newServer()
	clientID, code := connect(t, s, "1", "boss", "hc-key-boss")
	_, tok := exchange(s, clientID, code, verifier)
	if c, err := authenticate(s, tok["access_token"].(string)); err != nil || !c.Admin || c.UserID != "1" {
		t.Fatalf("admin: %+v %v", c, err)
	}
	if status, _ := refresh(s, clientID, tok["refresh_token"].(string)); status != 200 {
		t.Fatalf("admin refresh: %d", status)
	}

	_, out := register(t, s, callback)
	req, err := parse(s, authorizeQuery(out["client_id"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connect(t.Context(), req, "9", "mallory", "hc-key-m"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("mallory: %v", err)
	}
	var u User
	if err := st.Get(t.Context(), store.Key{PK: "USER#9", SK: "META"}, &u); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("mallory's record: %v", err)
	}
}

// racy is a store whose next transaction first runs meanwhile, as if
// someone else had acted just before it.
type racy struct {
	store.Store
	meanwhile func()
}

func (r *racy) Transact(ctx context.Context, ops ...store.Op) error {
	if f := r.meanwhile; f != nil {
		r.meanwhile = nil
		f()
	}
	return r.Store.Transact(ctx, ops...)
}

// A connect that races a removal doesn't store the key of someone just
// removed; one that races another change to the list goes through.
func TestConnectRacesTheAllowlist(t *testing.T) {
	s, st, _ := newServer()
	other := *s // the admin acting meanwhile, on the same table
	r := &racy{Store: st}
	s.Store = r
	_, out := register(t, s, callback)
	req, err := parse(s, authorizeQuery(out["client_id"].(string)))
	if err != nil {
		t.Fatal(err)
	}

	r.meanwhile = func() {
		if _, err := other.Allow(t.Context(), AllowedUser{ID: "8", Username: "sister"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Connect(t.Context(), req, "7", "adam", "hc-key-1"); err != nil {
		t.Fatalf("connect racing an addition: %v", err)
	}

	r.meanwhile = func() {
		if removed, err := other.Disallow(t.Context(), "adam"); removed == nil || err != nil {
			t.Fatalf("disallow: %v %v", removed, err)
		}
	}
	if _, err := s.Connect(t.Context(), req, "7", "adam", "hc-key-2"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("connect racing the removal: %v", err)
	}
	var u User
	if err := st.Get(t.Context(), store.Key{PK: "USER#7", SK: "META"}, &u); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("user record after the race: %+v %v", u, err)
	}
}

// failing is a store whose reads of one key fail, as DynamoDB can.
type failing struct {
	store.Store
	key store.Key
}

func (f failing) Get(ctx context.Context, key store.Key, out any) error {
	if key == f.key {
		return errors.New("throttled")
	}
	return f.Store.Get(ctx, key, out)
}

// A refresh that can't read the allowlist fails as the server's error,
// keeping the refresh token, so the client tries again later instead of
// dropping the connection.
func TestRefreshWhenTheAllowlistCantBeRead(t *testing.T) {
	s, st, _ := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")
	_, tok := exchange(s, clientID, code, verifier)
	s.Store = failing{Store: st, key: allowlistKey}
	if status, out := refresh(s, clientID, tok["refresh_token"].(string)); status != 500 || out["error"] != "server_error" {
		t.Fatalf("refresh: %d %v", status, out)
	}
	if _, err := authenticate(s, tok["access_token"].(string)); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("authenticate: %v", err)
	}
	// The refresh token survived for the retry.
	s.Store = st
	if status, out := refresh(s, clientID, tok["refresh_token"].(string)); status != 200 {
		t.Fatalf("retried refresh: %d %v", status, out)
	}
}

// Two admins changing the list at once both get their change.
func TestAllowlistEditsDontLoseEachOther(t *testing.T) {
	s, st, _ := newServer()
	other := *s
	r := &racy{Store: st}
	s.Store = r
	r.meanwhile = func() {
		if _, err := other.Allow(t.Context(), AllowedUser{ID: "8", Username: "sister"}); err != nil {
			t.Fatal(err)
		}
	}
	if added, err := s.Allow(t.Context(), AllowedUser{ID: "9", Username: "nick"}); !added || err != nil {
		t.Fatalf("allow: %v %v", added, err)
	}
	l, err := s.Allowlist(t.Context())
	if err != nil || len(l.Users) != 3 || l.Version != 3 {
		t.Fatalf("list: %+v %v", l, err)
	}
}

// A deleted user record means no more tokens, not just no more calls.
func TestDeletedUserCannotRefresh(t *testing.T) {
	s, st, _ := newServer()
	clientID, code := connect(t, s, "7", "adam", "hc-key-1")
	_, tok := exchange(s, clientID, code, verifier)
	if err := st.Delete(t.Context(), store.Key{PK: "USER#7", SK: "META"}); err != nil {
		t.Fatal(err)
	}
	if status, out := refresh(s, clientID, tok["refresh_token"].(string)); status != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("refresh for a deleted user: %d %v", status, out)
	}
}

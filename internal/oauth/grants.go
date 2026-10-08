package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

// User is one person, keyed by their Hardcover user ID, with their
// Hardcover key sealed to that ID and the ID of the key that sealed it.
// All of their grants use it, so reconnecting with a new key updates every
// connection they have.
type User struct {
	ID            string    `dynamodbav:"user_id"`
	Username      string    `dynamodbav:"username"`
	KeyCiphertext []byte    `dynamodbav:"key_ciphertext"`
	KeySealedBy   string    `dynamodbav:"key_sealed_by"`
	KeyUpdatedAt  time.Time `dynamodbav:"key_updated_at"`
	CreatedAt     time.Time `dynamodbav:"created_at"`
}

func (u User) Key() (string, string) { return "USER#" + u.ID, "META" }
func (User) ItemType() string        { return "user" }

// Grant is one connection: a client acting for a user. Tokens point at
// it; deleting it revokes them all.
type Grant struct {
	ID         string    `dynamodbav:"id"`
	ClientID   string    `dynamodbav:"client_id"`
	ClientName string    `dynamodbav:"client_name"`
	UserID     string    `dynamodbav:"user_id"`
	CreatedAt  time.Time `dynamodbav:"created_at"`
}

func (g Grant) Key() (string, string) { return "GRANT#" + g.ID, "META" }
func (Grant) ItemType() string        { return "grant" }

type authCode struct {
	Hash        string    `dynamodbav:"hash"`
	ClientID    string    `dynamodbav:"client_id"`
	RedirectURI string    `dynamodbav:"redirect_uri"`
	Challenge   string    `dynamodbav:"challenge"`
	GrantID     string    `dynamodbav:"grant_id"`
	Expires     time.Time `dynamodbav:"expires"`
}

func (c authCode) Key() (string, string) { return "AUTHCODE#" + c.Hash, "META" }
func (authCode) ItemType() string        { return "auth_code" }
func (c authCode) ExpiresAt() time.Time  { return c.Expires }

const (
	kindAccess  = "access"
	kindRefresh = "refresh"
)

type token struct {
	Hash     string    `dynamodbav:"hash"`
	Kind     string    `dynamodbav:"kind"`
	GrantID  string    `dynamodbav:"grant_id"`
	ClientID string    `dynamodbav:"client_id"`
	Issued   time.Time `dynamodbav:"issued"`
	Expires  time.Time `dynamodbav:"expires"`
}

func (t token) Key() (string, string) { return "TOKEN#" + t.Hash, "META" }
func (token) ItemType() string        { return "token" }
func (t token) ExpiresAt() time.Time  { return t.Expires }

// Connect records a successful connect, all in one transaction: the user
// with their key sealed (replacing any earlier key), a grant for this
// client, the client made permanent, and a single-use authorization code,
// which it returns. The caller has already checked the key with Hardcover.
// Someone who is neither an admin nor on the allowlist gets ErrNotAllowed,
// and the transaction fails if they are taken off the list before it
// lands.
func (s *Server) Connect(ctx context.Context, req *AuthorizeRequest, userID, username, key string) (string, error) {
	cond, err := s.stillAllowed(ctx, userID)
	if err != nil {
		return "", err
	}
	now := s.now()
	sealed, err := s.Sealer.Seal(ctx, userID, []byte(key))
	if err != nil {
		return "", fmt.Errorf("seal key: %w", err)
	}
	u := User{ID: userID, Username: username, KeyCiphertext: sealed.Ciphertext, KeySealedBy: sealed.KeyID, KeyUpdatedAt: now, CreatedAt: now}
	var prev User
	switch err := s.Store.Get(ctx, store.Key{PK: "USER#" + userID, SK: "META"}, &prev); {
	case err == nil:
		u.CreatedAt = prev.CreatedAt
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	client := req.Client
	client.Expires = nil
	for attempt := 1; ; attempt++ {
		g := Grant{ID: newID(), ClientID: client.ID, ClientName: client.Name, UserID: userID, CreatedAt: now}
		code := newToken()
		c := authCode{Hash: hash(code), ClientID: client.ID, RedirectURI: req.RedirectURI, Challenge: req.Challenge, GrantID: g.ID, Expires: now.Add(AuthCodeTTL)}
		ops := append([]store.Op{{Put: u}, {Put: client}, {Put: g}, {Put: c, IfAbsent: true}}, cond...)
		err := s.Store.Transact(ctx, ops...)
		if !errors.Is(err, store.ErrConflict) || len(cond) == 0 || attempt == 3 {
			if err != nil {
				return "", err
			}
			return code, nil
		}
		// The allowlist changed meanwhile: look again.
		if cond, err = s.stillAllowed(ctx, userID); err != nil {
			return "", err
		}
	}
}

// Token is the token endpoint.
func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	client, err := s.client(ctx, r.PostFormValue("client_id"))
	if err != nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	if client.SecretHash != "" && subtle.ConstantTimeCompare([]byte(hash(r.PostFormValue("client_secret"))), []byte(client.SecretHash)) != 1 {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "bad client secret")
		return
	}
	now := s.now()
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		var c authCode
		key := store.Key{PK: "AUTHCODE#" + hash(r.PostFormValue("code")), SK: "META"}
		if err := s.Store.Get(ctx, key, &c); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown code")
			return
		}
		if !s.usable(w, ctx, c.GrantID) {
			return
		}
		// Single use: whoever deletes it first wins.
		if err := s.Store.Delete(ctx, key); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code already used")
			return
		}
		if c.ClientID != client.ID || now.After(c.Expires) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code expired or wrong client")
			return
		}
		if uri := r.PostFormValue("redirect_uri"); uri != "" && uri != c.RedirectURI {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
			return
		}
		if !verifyPKCE(r.PostFormValue("code_verifier"), c.Challenge) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}
		s.issue(w, ctx, c.GrantID, client.ID, now)
	case "refresh_token":
		var t token
		key := store.Key{PK: "TOKEN#" + hash(r.PostFormValue("refresh_token")), SK: "META"}
		if err := s.Store.Get(ctx, key, &t); err != nil || t.Kind != kindRefresh || t.ClientID != client.ID || now.After(t.Expires) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "bad refresh token")
			return
		}
		// Checked before the old token dies, so a client that gets a
		// server error can try again with it.
		if !s.usable(w, ctx, t.GrantID) {
			return
		}
		if err := s.Store.Delete(ctx, key); err != nil { // rotation: the old one dies
			oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already used")
			return
		}
		s.issue(w, ctx, t.GrantID, client.ID, now)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

// usable reports whether a grant may still get tokens, writing the error
// if not. A grant is only as good as its user: gone from the table or off
// the allowlist, it gets no more tokens. Failing to read them is the
// server's problem, not a revocation, which the client would make the
// person reconnect for.
func (s *Server) usable(w http.ResponseWriter, ctx context.Context, grantID string) bool {
	g, err := s.grant(ctx, grantID)
	var u *User
	if err == nil {
		u, err = s.user(ctx, g.UserID)
	}
	ok := false
	if err == nil {
		ok, err = s.allowed(ctx, u.ID)
	}
	switch {
	case errors.Is(err, store.ErrNotFound), err == nil && !ok:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "grant revoked")
		return false
	case err != nil:
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return false
	}
	return true
}

func (s *Server) issue(w http.ResponseWriter, ctx context.Context, grantID, clientID string, now time.Time) {
	access, refresh := newToken(), newToken()
	err := s.Store.Transact(ctx,
		store.Op{Put: token{Hash: hash(access), Kind: kindAccess, GrantID: grantID, ClientID: clientID, Issued: now, Expires: now.Add(AccessTokenTTL)}},
		store.Op{Put: token{Hash: hash(refresh), Kind: kindRefresh, GrantID: grantID, ClientID: clientID, Issued: now, Expires: now.Add(RefreshTokenTTL)}},
	)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": int(AccessTokenTTL.Seconds()),
		"refresh_token": refresh, "scope": Scope,
	})
}

// Revoke is RFC 7009: revoking either token kind revokes the whole grant.
func (s *Server) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	var t token
	if err := s.Store.Get(r.Context(), store.Key{PK: "TOKEN#" + hash(r.PostFormValue("token")), SK: "META"}, &t); err == nil {
		_ = s.Store.Delete(r.Context(), store.Key{PK: "GRANT#" + t.GrantID, SK: "META"})
	}
	w.WriteHeader(http.StatusOK) // always, per the RFC
}

func (s *Server) user(ctx context.Context, id string) (*User, error) {
	var u User
	if err := s.Store.Get(ctx, store.Key{PK: "USER#" + id, SK: "META"}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Server) grant(ctx context.Context, id string) (*Grant, error) {
	var g Grant
	if err := s.Store.Get(ctx, store.Key{PK: "GRANT#" + id, SK: "META"}, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// Caller is who an MCP request acts for, with their Hardcover key opened.
type Caller struct {
	UserID   string
	Username string
	GrantID  string
	Key      string
	Admin    bool // may manage the allowlist
}

// ErrUnauthorized is a missing, unknown, expired or revoked token.
var ErrUnauthorized = errors.New("oauth: unauthorized")

// Authenticate resolves a bearer token to its caller: token, grant, user
// (still on the allowlist), then the user's key opened. A token that leads
// nowhere is ErrUnauthorized; any other error is the server's own failure.
func (s *Server) Authenticate(ctx context.Context, r *http.Request) (*Caller, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return nil, ErrUnauthorized
	}
	var t token
	if err := s.Store.Get(ctx, store.Key{PK: "TOKEN#" + hash(strings.TrimSpace(h[7:])), SK: "META"}, &t); err != nil {
		return nil, unauthorized(err)
	}
	if t.Kind != kindAccess || !s.now().Before(t.Expires) {
		return nil, ErrUnauthorized
	}
	g, err := s.grant(ctx, t.GrantID)
	if err != nil {
		return nil, unauthorized(err)
	}
	u, err := s.user(ctx, g.UserID)
	if err != nil {
		return nil, unauthorized(err)
	}
	switch ok, err := s.allowed(ctx, u.ID); {
	case err != nil:
		return nil, err
	case !ok:
		return nil, ErrUnauthorized
	}
	key, stale, err := s.Sealer.Open(ctx, u.ID, sealer.Sealed{Ciphertext: u.KeyCiphertext, KeyID: u.KeySealedBy})
	if err != nil {
		return nil, fmt.Errorf("open key of user %s: %w", u.ID, err)
	}
	if stale {
		// A previous key sealed it. Failing to seal it again isn't this
		// request's problem: the next one tries again.
		if err := s.reseal(ctx, u, key); err != nil && !errors.Is(err, store.ErrConflict) {
			slog.Warn("reseal", "user", u.ID, "err", err)
		}
	}
	return &Caller{UserID: u.ID, Username: u.Username, GrantID: g.ID, Key: string(key), Admin: s.IsAdmin(u.ID)}, nil
}

// reseal stores u's Hardcover key sealed again under the current key,
// unless the person has reconnected since u was read (ErrConflict): the
// new key they gave then must not be overwritten with this older one.
func (s *Server) reseal(ctx context.Context, u *User, key []byte) error {
	sealed, err := s.Sealer.Seal(ctx, u.ID, key)
	if err != nil {
		return err
	}
	next := *u
	next.KeyCiphertext, next.KeySealedBy = sealed.Ciphertext, sealed.KeyID
	return s.Store.Transact(ctx, store.Op{Put: next, IfMatch: &store.Match{Attr: "key_updated_at", Value: u.KeyUpdatedAt}})
}

func unauthorized(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrUnauthorized
	}
	return err
}

// Challenge writes the 401 that tells an MCP client where to authorize.
func (s *Server) Challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, s.Issuer))
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
}

// MintAccessToken issues an access token for an existing grant without
// the code exchange, for tests. The token is returned once and stored
// hashed.
func (s *Server) MintAccessToken(ctx context.Context, grantID string) (string, error) {
	g, err := s.grant(ctx, grantID)
	if err != nil {
		return "", err
	}
	now := s.now()
	access := newToken()
	if err := s.Store.Put(ctx, token{Hash: hash(access), Kind: kindAccess, GrantID: g.ID, ClientID: g.ClientID, Issued: now, Expires: now.Add(AccessTokenTTL)}); err != nil {
		return "", err
	}
	return access, nil
}

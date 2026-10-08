// Package oauth makes Dustjacket an OAuth 2.1 authorization server for MCP
// clients: authorization code with PKCE (S256), opaque tokens stored
// hashed, 1-hour access tokens with rotating 30-day refresh tokens, and
// dynamic client registration restricted to allowed redirect hosts. There
// is no login: on the connect page a person pastes their Hardcover key,
// and a grant is what lets a client use that key, sealed, from then on.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/sealer"
	"github.com/adamrothman/dustjacket/internal/store"
)

const (
	// Scope is the one scope there is: using the person's Hardcover key.
	Scope = "hardcover"

	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	AuthCodeTTL     = 10 * time.Minute
	// UnusedClientTTL is how long a registered client lives if nobody
	// connects through it (docs/decisions.md 7).
	UnusedClientTTL = 24 * time.Hour
)

// Client is a registered OAuth client. Expires is set until someone
// connects through it, and cleared then.
type Client struct {
	ID           string     `dynamodbav:"id"`
	SecretHash   string     `dynamodbav:"secret_hash,omitempty"`
	Name         string     `dynamodbav:"name"`
	RedirectURIs []string   `dynamodbav:"redirect_uris"`
	CreatedAt    time.Time  `dynamodbav:"created_at"`
	Expires      *time.Time `dynamodbav:"expires,omitempty"`
}

func (c Client) Key() (string, string) { return "OAUTHCLIENT#" + c.ID, "META" }
func (Client) ItemType() string        { return "oauth_client" }
func (c Client) ExpiresAt() time.Time {
	if c.Expires == nil {
		return time.Time{}
	}
	return *c.Expires
}

// Server is the authorization server.
type Server struct {
	Store  store.Store
	Sealer sealer.Sealer
	Issuer string // "https://dustjacket.rothman.tools"
	// Hosts a registered redirect URI may point at. Empty allows any
	// https host, which is not what you want in production.
	AllowedRedirectHosts []string
	// Admins are the Hardcover user IDs of the people who manage the
	// allowlist. They may always connect, and are never on the list
	// (docs/decisions.md 23).
	Admins []string
	Now    func() time.Time // time.Now when nil
}

func (s *Server) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Metadata is RFC 8414.
func (s *Server) Metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     s.Issuer,
		"authorization_endpoint":                     s.Issuer + "/oauth/authorize",
		"token_endpoint":                             s.Issuer + "/oauth/token",
		"revocation_endpoint":                        s.Issuer + "/oauth/revoke",
		"registration_endpoint":                      s.Issuer + "/oauth/register",
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_post"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"scopes_supported":                           []string{Scope},
	})
}

// ProtectedResourceMetadata is RFC 9728, which MCP clients use to find
// the authorization server for /mcp.
func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.Issuer + "/mcp",
		"authorization_servers":    []string{s.Issuer},
		"scopes_supported":         []string{Scope},
		"bearer_methods_supported": []string{"header"},
	})
}

// Register is RFC 7591 dynamic client registration, open but confined to
// the allowed redirect hosts. A client gives nothing until someone on the
// allowlist connects through it, and expires unused after a day.
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "bad JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !s.redirectAllowed(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URI host not allowed: "+u)
			return
		}
	}
	now := s.now()
	expires := now.Add(UnusedClientTTL)
	c := Client{ID: newID(), Name: strings.TrimSpace(req.ClientName), RedirectURIs: req.RedirectURIs, CreatedAt: now, Expires: &expires}
	if c.Name == "" {
		c.Name = "unnamed client"
	}
	resp := map[string]any{
		"client_id":                  c.ID,
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"client_id_issued_at":        c.CreatedAt.Unix(),
	}
	if req.TokenEndpointAuthMethod == "client_secret_post" || req.TokenEndpointAuthMethod == "client_secret_basic" {
		secret := newToken()
		c.SecretHash = hash(secret)
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
		resp["token_endpoint_auth_method"] = "client_secret_post"
	}
	if err := s.Store.PutIfAbsent(r.Context(), c); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Fragment != "" {
		return false
	}
	if len(s.AllowedRedirectHosts) == 0 {
		return true
	}
	for _, h := range s.AllowedRedirectHosts {
		if strings.EqualFold(u.Hostname(), h) {
			return true
		}
	}
	return false
}

// client loads a registered client; one past its expiry is as good as
// gone, since DynamoDB deletes expired items only eventually.
func (s *Server) client(ctx context.Context, id string) (*Client, error) {
	var c Client
	if err := s.Store.Get(ctx, store.Key{PK: "OAUTHCLIENT#" + id, SK: "META"}, &c); err != nil {
		return nil, err
	}
	if c.Expires != nil && !s.now().Before(*c.Expires) {
		return nil, store.ErrNotFound
	}
	return &c, nil
}

// AuthorizeRequest is a parsed, validated /oauth/authorize query.
type AuthorizeRequest struct {
	Client      Client
	RedirectURI string
	State       string
	Challenge   string
}

// ParseAuthorize validates the query. Errors that cannot be sent to the
// redirect URI (bad client, bad redirect) come back with a nil request,
// to show to the person; the rest come back as *RedirectError with the
// request, to redirect per RFC 6749 §4.1.2.1.
func (s *Server) ParseAuthorize(r *http.Request) (*AuthorizeRequest, error) {
	q := r.URL.Query()
	c, err := s.client(r.Context(), q.Get("client_id"))
	if err != nil {
		return nil, errors.New("unknown client_id")
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if !slices.Contains(c.RedirectURIs, redirect) {
		return nil, errors.New("redirect_uri is not registered for this client")
	}
	req := &AuthorizeRequest{Client: *c, RedirectURI: redirect, State: q.Get("state")}
	if q.Get("response_type") != "code" {
		return req, &RedirectError{Code: "unsupported_response_type", Desc: "only code is supported"}
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 {
		return req, &RedirectError{Code: "invalid_request", Desc: "PKCE S256 code_challenge required"}
	}
	req.Challenge = q.Get("code_challenge")
	for sc := range strings.FieldsSeq(q.Get("scope")) {
		if sc != Scope {
			return req, &RedirectError{Code: "invalid_scope", Desc: "unknown scope " + sc}
		}
	}
	if res := q.Get("resource"); res != "" && res != s.Issuer+"/mcp" {
		return req, &RedirectError{Code: "invalid_target", Desc: "unknown resource"}
	}
	return req, nil
}

// RedirectError goes back to the client via the redirect URI.
type RedirectError struct{ Code, Desc string }

func (e *RedirectError) Error() string { return e.Code + ": " + e.Desc }

// Redirect sends the client an error or a code.
func (req *AuthorizeRequest) Redirect(w http.ResponseWriter, r *http.Request, params map[string]string) {
	u, _ := url.Parse(req.RedirectURI)
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// newID is a 128-bit random identifier.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// newToken is a 256-bit random token, URL-safe.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hash is how tokens and codes are stored: never the token itself.
func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

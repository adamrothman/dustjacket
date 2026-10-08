package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/adamrothman/dustjacket/internal/hardcover"
	"github.com/adamrothman/dustjacket/internal/oauth"
)

// What the connect page says when a key does not work out.
const (
	msgNoKey       = "Paste your Hardcover API key."
	msgBadKey      = "Hardcover didn't accept that key. Check that you copied all of it and that it hasn't expired."
	msgNoScope     = "That key can't read your Hardcover profile. Create one with the link above."
	msgUnavailable = "Couldn't reach Hardcover. Try again in a moment."
)

type connectView struct {
	ClientName string
	Query      string // the authorize query, re-validated on post
	KeyURL     string
	Message    string
}

// authorize shows the connect page for a valid authorize request.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	req, err := s.OAuth.ParseAuthorize(r)
	if err != nil {
		s.authorizeError(w, r, req, err)
		return
	}
	s.render(w, http.StatusOK, "connect", connectView{ClientName: req.Client.Name, Query: r.URL.RawQuery, KeyURL: hardcover.KeyURL})
}

func (s *Server) authorizeError(w http.ResponseWriter, r *http.Request, req *oauth.AuthorizeRequest, err error) {
	var re *oauth.RedirectError
	if errors.As(err, &re) && req != nil {
		req.Redirect(w, r, map[string]string{"error": re.Code, "error_description": re.Desc})
		return
	}
	s.render(w, http.StatusBadRequest, "error", "This connect link isn't valid: "+err.Error()+". Start again from Claude.")
}

// connect takes the posted key: asks Hardcover whose it is, then records
// the connection, if they are allowed, and sends the client its code.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		s.render(w, http.StatusForbidden, "error", "This form has to be sent from Dustjacket's own page.")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "error", "That form didn't arrive intact. Start again from Claude.")
		return
	}
	// The original query rides along in the form so the request is
	// re-validated rather than trusted.
	q := r.PostFormValue("q")
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q
	req, err := s.OAuth.ParseAuthorize(r2)
	if err != nil {
		s.authorizeError(w, r, req, err)
		return
	}
	if r.PostFormValue("decision") == "cancel" {
		req.Redirect(w, r, map[string]string{"error": "access_denied"})
		return
	}
	view := connectView{ClientName: req.Client.Name, Query: q, KeyURL: hardcover.KeyURL}
	key := normalizeKey(r.PostFormValue("key"))
	if key == "" {
		view.Message = msgNoKey
		s.render(w, http.StatusBadRequest, "connect", view)
		return
	}
	user, err := s.Hardcover.Me(r.Context(), key)
	if err != nil {
		status, msg := meFailure(err)
		s.log().Warn("connect: key rejected", "err", err)
		view.Message = msg
		s.render(w, status, "connect", view)
		return
	}
	code, err := s.OAuth.Connect(r.Context(), req, strconv.Itoa(user.ID), user.Username, key)
	if errors.Is(err, oauth.ErrNotAllowed) {
		s.log().Warn("connect: not on the allowlist", "username", user.Username, "user_id", user.ID)
		view.Message = "@" + user.Username + " isn't on this server's allowlist. Ask Adam to add you."
		s.render(w, http.StatusForbidden, "connect", view)
		return
	}
	if err != nil {
		s.log().Error("connect", "err", err)
		s.render(w, http.StatusInternalServerError, "error", "Something went wrong saving the connection. Try again from Claude.")
		return
	}
	s.log().Info("connected", "username", user.Username, "client", req.Client.ID)
	req.Redirect(w, r, map[string]string{"code": code})
}

// meFailure says what the connect page tells the person when Hardcover's
// `me` fails for their key.
func meFailure(err error) (int, string) {
	var he *hardcover.Error
	switch {
	case errors.Is(err, hardcover.ErrNoUser):
		return http.StatusBadRequest, msgBadKey
	case errors.Is(err, hardcover.ErrQuery):
		return http.StatusBadRequest, msgNoScope
	case !errors.As(err, &he):
		return http.StatusBadGateway, msgUnavailable
	case he.Status == http.StatusForbidden && he.Code == "insufficient_scope":
		return http.StatusBadRequest, msgNoScope
	case he.Retryable() || he.Status == http.StatusTooManyRequests:
		return http.StatusBadGateway, msgUnavailable
	}
	return http.StatusBadRequest, msgBadKey
}

// sameOrigin requires the form to have been posted from our own page.
// Browsers send Origin with every form post.
func (s *Server) sameOrigin(r *http.Request) bool {
	u, err := url.Parse(s.BaseURL)
	return err == nil && r.Header.Get("Origin") == u.Scheme+"://"+u.Host
}

// normalizeKey trims the pasted key and drops a "Bearer " prefix, in case
// it was copied with one.
func normalizeKey(k string) string {
	k = strings.TrimSpace(k)
	if len(k) > 7 && strings.EqualFold(k[:7], "bearer ") {
		k = strings.TrimSpace(k[7:])
	}
	return k
}

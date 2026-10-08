package oauth

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/adamrothman/dustjacket/internal/store"
)

// Allowlist is who may connect and keep using their connections, besides
// the admins, keyed by Hardcover user ID (docs/decisions.md 23). It is one
// item, so checking it is one read, and every change to it is a write
// conditioned on the version that was read.
type Allowlist struct {
	Version   int                    `dynamodbav:"version"`
	Users     map[string]AllowedUser `dynamodbav:"users"`
	UpdatedAt time.Time              `dynamodbav:"updated_at"`
}

func (Allowlist) Key() (string, string) { return "ALLOWLIST", "META" }
func (Allowlist) ItemType() string      { return "allowlist" }

// AllowedUser is one person on the allowlist. The ID is what is checked;
// the username is as Hardcover had it when they were added, for reading
// the list.
type AllowedUser struct {
	ID       string    `dynamodbav:"user_id"`
	Username string    `dynamodbav:"username"`
	AddedAt  time.Time `dynamodbav:"added_at"`
	AddedBy  string    `dynamodbav:"added_by"`
}

var allowlistKey = store.Key{PK: "ALLOWLIST", SK: "META"}

var (
	// ErrNotAllowed is someone who is neither an admin nor on the
	// allowlist trying to connect.
	ErrNotAllowed = errors.New("oauth: not on the allowlist")
	// ErrAdmin is an admin being added to or removed from the allowlist,
	// which doesn't hold them: they are allowed by configuration.
	ErrAdmin = errors.New("oauth: admins are set in configuration, not on the allowlist")
)

// IsAdmin reports whether a Hardcover user ID is an admin's.
func (s *Server) IsAdmin(userID string) bool {
	return userID != "" && slices.Contains(s.Admins, userID)
}

// Allowlist reads the allowlist. Before anyone has been added, it is
// empty, at version 0.
func (s *Server) Allowlist(ctx context.Context) (*Allowlist, error) {
	var l Allowlist
	if err := s.Store.Get(ctx, allowlistKey, &l); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if l.Users == nil {
		l.Users = map[string]AllowedUser{}
	}
	return &l, nil
}

// allowed reports whether a Hardcover user may connect and keep using
// their connections: an admin, or someone on the allowlist.
func (s *Server) allowed(ctx context.Context, userID string) (bool, error) {
	if s.IsAdmin(userID) {
		return true, nil
	}
	l, err := s.Allowlist(ctx)
	if err != nil {
		return false, err
	}
	_, ok := l.Users[userID]
	return ok, nil
}

// stillAllowed is what Connect needs before it writes: nothing for an
// admin, ErrNotAllowed for someone not on the allowlist, and otherwise
// the op that fails the write if the allowlist changes before it lands,
// so a connect can't save the key of someone removed meanwhile.
func (s *Server) stillAllowed(ctx context.Context, userID string) ([]store.Op, error) {
	if s.IsAdmin(userID) {
		return nil, nil
	}
	l, err := s.Allowlist(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := l.Users[userID]; !ok {
		return nil, ErrNotAllowed
	}
	return []store.Op{{Check: &allowlistKey, IfMatch: &store.Match{Attr: "version", Value: l.Version}}}, nil
}

// Allow adds someone to the allowlist; added is false when they were on
// it already.
func (s *Server) Allow(ctx context.Context, u AllowedUser) (added bool, err error) {
	if s.IsAdmin(u.ID) {
		return false, ErrAdmin
	}
	return s.editAllowlist(ctx, func(users map[string]AllowedUser) (bool, []store.Op) {
		if _, ok := users[u.ID]; ok {
			return false, nil
		}
		u.AddedAt = s.now()
		users[u.ID] = u
		return true, nil
	})
}

// Disallow takes the person with this username (ignoring case and a
// leading @) off the allowlist and, in the same write, deletes their user
// record and with it their sealed Hardcover key, so every connection they
// have stops working. It returns who was removed, or nil if nobody on the
// allowlist has that username.
func (s *Server) Disallow(ctx context.Context, username string) (*AllowedUser, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	var removed *AllowedUser
	_, err := s.editAllowlist(ctx, func(users map[string]AllowedUser) (bool, []store.Op) {
		removed = nil
		for id, u := range users {
			if strings.EqualFold(u.Username, username) {
				removed = &u
				delete(users, id)
				return true, []store.Op{{Delete: &store.Key{PK: "USER#" + id, SK: "META"}}}
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// editAllowlist reads the allowlist, lets edit change its users, and
// writes it back with any ops edit adds, unless edit changed nothing. If
// someone else changed the list in between, it starts again, a few times.
func (s *Server) editAllowlist(ctx context.Context, edit func(map[string]AllowedUser) (bool, []store.Op)) (bool, error) {
	for range 3 {
		l, err := s.Allowlist(ctx)
		if err != nil {
			return false, err
		}
		users := maps.Clone(l.Users)
		changed, more := edit(users)
		if !changed {
			return false, nil
		}
		put := store.Op{Put: Allowlist{Version: l.Version + 1, Users: users, UpdatedAt: s.now()}}
		if l.Version == 0 {
			put.IfAbsent = true
		} else {
			put.IfMatch = &store.Match{Attr: "version", Value: l.Version}
		}
		err = s.Store.Transact(ctx, append([]store.Op{put}, more...)...)
		if !errors.Is(err, store.ErrConflict) {
			return err == nil, err
		}
	}
	return false, store.ErrConflict
}

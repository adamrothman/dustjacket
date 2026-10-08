package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// User is the person a key belongs to.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// ErrNoUser is a key that authenticated but whose `me` is empty.
var ErrNoUser = errors.New("hardcover: me returned no user")

// ErrQuery is GraphQL rejecting the `me` query, which is valid, so the
// key lacks permission to run it.
var ErrQuery = errors.New("hardcover: me failed")

// Me asks Hardcover whose key this is. `me` returns a list whose one
// element is the key's owner.
func (c *Client) Me(ctx context.Context, key string) (*User, error) {
	res, err := c.Do(ctx, key, Request{Query: "query Me { me { id username } }"})
	if err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			Me []User `json:"me"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, err
	}
	if len(body.Errors) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrQuery, body.Errors[0].Message)
	}
	if len(body.Data.Me) == 0 || body.Data.Me[0].Username == "" {
		return nil, ErrNoUser
	}
	return &body.Data.Me[0], nil
}

// ErrNoSuchUser is a username Hardcover has no user for, as far as the
// key can see.
var ErrNoSuchUser = errors.New("hardcover: no user with that username")

// UserByUsername looks someone up by username, which Hardcover matches
// ignoring case, and returns them with the username as Hardcover has it
// (docs/hardcover-notes.md L21).
func (c *Client) UserByUsername(ctx context.Context, key, username string) (*User, error) {
	res, err := c.Do(ctx, key, Request{
		Query:     "query User($username: citext!) { users(where: {username: {_eq: $username}}) { id username } }",
		Variables: map[string]any{"username": username},
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			Users []User `json:"users"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(res.Body, &body); err != nil {
		return nil, err
	}
	if len(body.Errors) > 0 {
		return nil, fmt.Errorf("hardcover: users failed: %s", body.Errors[0].Message)
	}
	if len(body.Data.Users) == 0 || body.Data.Users[0].Username == "" {
		return nil, ErrNoSuchUser
	}
	return &body.Data.Users[0], nil
}

// Scopes is what a Dustjacket key needs: the smallest set covering the
// use cases in docs/decisions.md. KeyURL pre-selects them.
var Scopes = []string{
	"read:me:content",
	"read:catalog",
	"read:library", "write:library",
	"read:journal",
	"read:lists", "write:lists",
	"read:goals", "write:goals",
	"read:social", "write:social",
	"read:users",
	"write:reviews",
}

// KeyURL opens Hardcover's new-key page with Scopes selected.
var KeyURL = "https://hardcover.app/account/api/keys/new?scope=" + strings.Join(Scopes, "+")

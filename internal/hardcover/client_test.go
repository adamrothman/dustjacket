package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// reply is one canned answer from the fake Hardcover.
type reply struct {
	status int
	header map[string]string
	body   string
}

func fake(t *testing.T, r reply) (*Client, *http.Request, *[]byte) {
	t.Helper()
	var got http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = *req
		body, _ = io.ReadAll(req.Body)
		for k, v := range r.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(r.status)
		io.WriteString(w, r.body)
	}))
	t.Cleanup(srv.Close)
	return &Client{URL: srv.URL, UserAgent: "dustjacket/test"}, &got, &body
}

func TestDoSendsTheRequest(t *testing.T) {
	c, got, body := fake(t, reply{status: 200, header: map[string]string{"RateLimit": `"Free";r=59;t=60`}, body: `{"data":{"me":[{"id":1}]}}`})
	res, err := c.Do(context.Background(), "k-123", Request{Query: "query Q($n: Int!) { me { id } }", Variables: map[string]any{"n": 1}, OperationName: "Q"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.Header.Get("Authorization") != "Bearer k-123" || got.Header.Get("User-Agent") != "dustjacket/test" || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request: %s %v", got.Method, got.Header)
	}
	var sent map[string]any
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["query"] != "query Q($n: Int!) { me { id } }" || sent["operationName"] != "Q" || sent["variables"].(map[string]any)["n"] != float64(1) {
		t.Fatalf("body: %s", *body)
	}
	if string(res.Body) != `{"data":{"me":[{"id":1}]}}` || res.RateLimit != `"Free";r=59;t=60` || res.HasErrors {
		t.Fatalf("response: %+v", res)
	}
}

func TestDoGraphQLErrors(t *testing.T) {
	c, _, _ := fake(t, reply{status: 200, body: `{"errors":[{"message":"field 'nope' not found"}]}`})
	res, err := c.Do(context.Background(), "k", Request{Query: "{ nope }"})
	if err != nil || !res.HasErrors {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDoRefusals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reply     reply
		want      Error
		retryable bool
	}{
		{"invalid token", reply{status: 401, body: `{"error":"invalid_token"}`}, Error{Status: 401, Code: "invalid_token"}, false},
		{"missing scope", reply{status: 403, body: `{"error":"insufficient_scope","scope":"read:lists"}`}, Error{Status: 403, Code: "insufficient_scope", Scope: "read:lists"}, false},
		{"too many fields", reply{status: 403, body: `{"error":"top_level_limit_exceeded"}`}, Error{Status: 403, Code: "top_level_limit_exceeded"}, false},
		{"rate limited", reply{status: 429, header: map[string]string{"Retry-After": "12"}, body: `{"error":"rate_limited"}`}, Error{Status: 429, Code: "rate_limited", RetryAfter: "12"}, false},
		{"unavailable", reply{status: 503, body: `upstream down`}, Error{Status: 503}, true},
		{"timeout", reply{status: 408, body: ``}, Error{Status: 408}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := fake(t, tc.reply)
			_, err := c.Do(context.Background(), "k", Request{Query: "{ me { id } }"})
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error %T %v", err, err)
			}
			if e.Status != tc.want.Status || e.Code != tc.want.Code || e.Scope != tc.want.Scope || e.RetryAfter != tc.want.RetryAfter {
				t.Fatalf("got %+v, want %+v", e, tc.want)
			}
			if e.Retryable() != tc.retryable {
				t.Fatalf("retryable = %v", e.Retryable())
			}
		})
	}
}

func TestDoNotJSON(t *testing.T) {
	c, _, _ := fake(t, reply{status: 200, body: `<html>oops</html>`})
	_, err := c.Do(context.Background(), "k", Request{Query: "{ me { id } }"})
	var e *Error
	if !errors.As(err, &e) || e.Err == nil || e.Retryable() {
		t.Fatalf("%v", err)
	}
}

func TestDoTooLarge(t *testing.T) {
	c, _, _ := fake(t, reply{status: 200, body: `{"data":"` + strings.Repeat("x", maxBody) + `"}`})
	_, err := c.Do(context.Background(), "k", Request{Query: "{ me { id } }"})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTooLarge {
		t.Fatalf("%v", err)
	}
}

func TestDoTimeoutIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slower than the caller's deadline, but not forever: Close waits
		// for handlers to return.
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (&Client{URL: srv.URL}).Do(ctx, "k", Request{Query: "{ me { id } }"})
	var e *Error
	if !errors.As(err, &e) || e.Status != 0 || !e.Retryable() {
		t.Fatalf("%v", err)
	}
}

func TestMe(t *testing.T) {
	c, _, body := fake(t, reply{status: 200, body: `{"data":{"me":[{"id":7,"username":"adam"}]}}`})
	u, err := c.Me(context.Background(), "k")
	if err != nil || u.ID != 7 || u.Username != "adam" {
		t.Fatalf("%+v %v", u, err)
	}
	if !strings.Contains(string(*body), "me { id username }") {
		t.Fatalf("query: %s", *body)
	}

	c, _, _ = fake(t, reply{status: 200, body: `{"data":{"me":[]}}`})
	if _, err := c.Me(context.Background(), "k"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("empty me: %v", err)
	}
	c, _, _ = fake(t, reply{status: 200, body: `{"errors":[{"message":"field 'me' not found in type: 'query_root'"}]}`})
	if _, err := c.Me(context.Background(), "k"); !errors.Is(err, ErrQuery) {
		t.Fatalf("graphql error: %v", err)
	}
	c, _, _ = fake(t, reply{status: 401, body: `{"error":"invalid_token"}`})
	var e *Error
	if _, err := c.Me(context.Background(), "k"); !errors.As(err, &e) || e.Status != 401 {
		t.Fatalf("401: %v", err)
	}
}

func TestUserByUsername(t *testing.T) {
	c, _, body := fake(t, reply{status: 200, body: `{"data":{"users":[{"id":7,"username":"adam"}]}}`})
	u, err := c.UserByUsername(context.Background(), "k", "ADAM")
	if err != nil || u.ID != 7 || u.Username != "adam" {
		t.Fatalf("%+v %v", u, err)
	}
	var sent Request
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Variables["username"] != "ADAM" || !strings.Contains(sent.Query, "users(where: {username: {_eq: $username}})") {
		t.Fatalf("sent %+v", sent)
	}

	c, _, _ = fake(t, reply{status: 200, body: `{"data":{"users":[]}}`})
	if _, err := c.UserByUsername(context.Background(), "k", "nobody"); !errors.Is(err, ErrNoSuchUser) {
		t.Fatalf("no such user: %v", err)
	}
	c, _, _ = fake(t, reply{status: 200, body: `{"errors":[{"message":"field 'users' not found in type: 'query_root'"}]}`})
	if _, err := c.UserByUsername(context.Background(), "k", "adam"); err == nil || errors.Is(err, ErrNoSuchUser) {
		t.Fatalf("graphql error: %v", err)
	}
	c, _, _ = fake(t, reply{status: 401, body: `{"error":"invalid_token"}`})
	var e *Error
	if _, err := c.UserByUsername(context.Background(), "k", "adam"); !errors.As(err, &e) || e.Status != 401 {
		t.Fatalf("401: %v", err)
	}
}

func TestKeyURL(t *testing.T) {
	want := "https://hardcover.app/account/api/keys/new?scope=read:me:content+read:catalog+read:library+write:library+read:journal+read:lists+write:lists+read:goals+write:goals+read:social+write:social+read:users+write:reviews"
	if KeyURL != want {
		t.Fatalf("KeyURL = %s", KeyURL)
	}
}

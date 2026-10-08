// Package hardcover is a small client for Hardcover's GraphQL API: it
// sends a document with one person's key and hands back the response
// body verbatim, or an *Error saying why Hardcover refused it.
package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultURL = "https://api.hardcover.app/v1/graphql"

// Timeout bounds one request. It sits inside the Lambda invocation's
// budget, leaving time to answer when Hardcover is slow.
const Timeout = 20 * time.Second

// maxBody caps how much of a response is read: far beyond what the MCP
// tools pass on, so a bigger one is refused rather than buffered.
const maxBody = 4 << 20

// CodeTooLarge is the Error.Code of a response over maxBody.
const CodeTooLarge = "response_too_large"

type Client struct {
	URL       string       // DefaultURL when empty
	UserAgent string       // sent when set
	HTTP      *http.Client // a client with Timeout when nil
}

type Request struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// Response is a request GraphQL ran, successfully or not.
type Response struct {
	Body      json.RawMessage // the JSON body, verbatim
	RateLimit string          // the RateLimit header, verbatim
	HasErrors bool            // the body has a non-empty "errors"
}

// Error is Hardcover refusing a request, or the request failing to get
// an answer at all (Status 0).
type Error struct {
	Status     int    // HTTP status; 0 for timeouts and network failures
	Code       string // the body's "error", e.g. "invalid_token"
	Scope      string // the body's "scope", with insufficient_scope
	RetryAfter string // the Retry-After header, with 429
	Body       string // the body, trimmed
	Err        error  // the cause, for failures without a usable answer
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil:
		return "hardcover: " + e.Err.Error()
	case e.Code != "":
		return fmt.Sprintf("hardcover: HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("hardcover: HTTP %d", e.Status)
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable is a timeout, a network failure, a 408 or a 5xx: sending
// the same request again may work.
func (e *Error) Retryable() bool {
	return e.Status == 0 || e.Status == http.StatusRequestTimeout || e.Status >= 500
}

func (c *Client) url() string {
	if c.URL == "" {
		return DefaultURL
	}
	return c.URL
}

func (c *Client) client() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: Timeout}
	}
	return c.HTTP
}

// Do sends req with key as the bearer token.
func (c *Client) Do(ctx context.Context, key string, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+key)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		hreq.Header.Set("User-Agent", c.UserAgent)
	}
	res, err := c.client().Do(hreq)
	if err != nil {
		return nil, &Error{Err: err}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, &Error{Status: res.StatusCode, Err: err}
	}
	if len(raw) > maxBody {
		return nil, &Error{Status: res.StatusCode, Code: CodeTooLarge}
	}
	if res.StatusCode != http.StatusOK {
		return nil, refusal(res, raw)
	}
	var probe struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &Error{Status: res.StatusCode, Body: trim(raw), Err: fmt.Errorf("response is not JSON: %w", err)}
	}
	return &Response{Body: raw, RateLimit: res.Header.Get("RateLimit"), HasErrors: len(probe.Errors) > 0}, nil
}

func refusal(res *http.Response, raw []byte) *Error {
	e := &Error{Status: res.StatusCode, RetryAfter: res.Header.Get("Retry-After"), Body: trim(raw)}
	var b struct {
		Error string `json:"error"`
		Scope string `json:"scope"`
	}
	if json.Unmarshal(raw, &b) == nil {
		e.Code, e.Scope = b.Error, b.Scope
	}
	return e
}

func trim(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

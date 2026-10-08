// Package reqlog carries attributes from deep inside a request's handling
// (the MCP tools) back out to the one log line the access log writes for
// the request.
package reqlog

import (
	"context"
	"sync"
)

// Fields collects a request's extra log attributes, as slog key-value
// pairs. A nil *Fields ignores Add, so handlers need not check.
type Fields struct {
	mu    sync.Mutex
	attrs []any
}

type ctxKey struct{}

// With returns a context carrying a new, empty Fields.
func With(ctx context.Context) (context.Context, *Fields) {
	f := &Fields{}
	return context.WithValue(ctx, ctxKey{}, f), f
}

// From returns the context's Fields, or nil.
func From(ctx context.Context) *Fields {
	f, _ := ctx.Value(ctxKey{}).(*Fields)
	return f
}

func (f *Fields) Add(attrs ...any) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attrs = append(f.attrs, attrs...)
}

// Attrs returns a copy of what was added.
func (f *Fields) Attrs() []any {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any(nil), f.attrs...)
}

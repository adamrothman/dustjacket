package reqlog

import (
	"context"
	"slices"
	"testing"
)

func TestFields(t *testing.T) {
	ctx, f := With(context.Background())
	From(ctx).Add("tool", "hardcover_query")
	From(ctx).Add("hc_status", 200)
	if got := f.Attrs(); !slices.Equal(got, []any{"tool", "hardcover_query", "hc_status", 200}) {
		t.Fatalf("attrs: %v", got)
	}
	// Without Fields in the context, Add and Attrs do nothing.
	From(context.Background()).Add("x", 1)
	if got := From(context.Background()).Attrs(); got != nil {
		t.Fatalf("nil fields: %v", got)
	}
}

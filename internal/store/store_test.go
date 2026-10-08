package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type thing struct {
	ID      string     `dynamodbav:"id"`
	Note    string     `dynamodbav:"note"`
	Expires *time.Time `dynamodbav:"expires,omitempty"`
}

func (t thing) Key() (string, string) { return "THING#" + t.ID, "META" }
func (thing) ItemType() string        { return "thing" }
func (t thing) ExpiresAt() time.Time {
	if t.Expires == nil {
		return time.Time{}
	}
	return *t.Expires
}

func key(id string) Key { return Key{"THING#" + id, "META"} }

func TestRoundTripAndConflicts(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := s.Put(ctx, thing{ID: "a", Note: "first"}); err != nil {
		t.Fatal(err)
	}
	var got thing
	if err := s.Get(ctx, key("a"), &got); err != nil || got.Note != "first" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if err := s.Get(ctx, key("b"), &got); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.PutIfAbsent(ctx, thing{ID: "a"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("put-if-absent over existing: %v", err)
	}
	if err := s.Delete(ctx, key("a")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestTransactIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := s.Put(ctx, thing{ID: "taken"}); err != nil {
		t.Fatal(err)
	}
	err := s.Transact(ctx, Op{Put: thing{ID: "new"}}, Op{Put: thing{ID: "taken"}, IfAbsent: true})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("transact: %v", err)
	}
	var got thing
	if err := s.Get(ctx, key("new"), &got); !errors.Is(err, ErrNotFound) {
		t.Fatal("a failed transaction wrote its other items")
	}
	if err := s.Transact(ctx, Op{Put: thing{ID: "new"}}, Op{Put: thing{ID: "other"}, IfAbsent: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, key("other"), &got); err != nil {
		t.Fatal(err)
	}
}

func TestTransactIfMatch(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := s.Put(ctx, thing{ID: "a", Note: "first"}); err != nil {
		t.Fatal(err)
	}
	// The stored note is still "first", so the write goes through.
	if err := s.Transact(ctx, Op{Put: thing{ID: "a", Note: "second"}, IfMatch: &Match{Attr: "note", Value: "first"}}); err != nil {
		t.Fatal(err)
	}
	// Now it is "second": a write expecting "first" is refused and writes nothing.
	err := s.Transact(ctx, Op{Put: thing{ID: "a", Note: "third"}, IfMatch: &Match{Attr: "note", Value: "first"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale match: %v", err)
	}
	var got thing
	if err := s.Get(ctx, key("a"), &got); err != nil || got.Note != "second" {
		t.Fatalf("after refused write: %+v %v", got, err)
	}
	// A missing item matches nothing.
	if err := s.Transact(ctx, Op{Put: thing{ID: "b"}, IfMatch: &Match{Attr: "note", Value: ""}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing item: %v", err)
	}
}

func TestTransactDeleteAndCheck(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := s.Put(ctx, thing{ID: "list", Note: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, thing{ID: "gone"}); err != nil {
		t.Fatal(err)
	}
	// A failed check writes nothing, deletes included.
	err := s.Transact(ctx, Op{Check: ptr(key("list")), IfMatch: &Match{Attr: "note", Value: "v0"}}, Op{Delete: ptr(key("gone"))}, Op{Put: thing{ID: "new"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale check: %v", err)
	}
	var got thing
	if err := s.Get(ctx, key("gone"), &got); err != nil {
		t.Fatal("a failed transaction deleted an item")
	}
	if err := s.Get(ctx, key("new"), &got); !errors.Is(err, ErrNotFound) {
		t.Fatal("a failed transaction wrote an item")
	}
	// A passing check lets the rest through, and leaves its item alone.
	// Deleting what isn't there is no failure.
	if err := s.Transact(ctx, Op{Check: ptr(key("list")), IfMatch: &Match{Attr: "note", Value: "v1"}}, Op{Delete: ptr(key("gone"))}, Op{Delete: ptr(key("never"))}, Op{Put: thing{ID: "new"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Get(ctx, key("gone"), &got); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete in a transaction left the item")
	}
	if err := s.Get(ctx, key("list"), &got); err != nil || got.Note != "v1" {
		t.Fatalf("checked item: %+v %v", got, err)
	}
	// An op must be exactly one thing, and a check must check something.
	for _, bad := range []Op{{}, {Put: thing{ID: "x"}, Delete: ptr(key("x"))}, {Check: ptr(key("x"))}} {
		if err := s.Transact(ctx, bad); err == nil || errors.Is(err, ErrConflict) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestTTLOnlyWhenSet(t *testing.T) {
	exp := time.Unix(1700000000, 0)
	r, err := Encode(thing{ID: "a", Expires: &exp})
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := r["ttl"].(*types.AttributeValueMemberN); !ok || n.Value != "1700000000" {
		t.Fatalf("ttl = %#v", r["ttl"])
	}
	if r["type"].(*types.AttributeValueMemberS).Value != "thing" || r.Key() != key("a") {
		t.Fatalf("keys and type: %#v", r)
	}
	r, err = Encode(thing{ID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r["ttl"]; ok {
		t.Fatal("ttl written for an item that does not expire")
	}
}

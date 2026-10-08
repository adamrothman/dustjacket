// Package store is the single-table persistence layer: one interface with
// a DynamoDB implementation and an in-memory one that shares the same
// attribute encoding, so tests exercise the real marshalling.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: condition failed")
)

// Key identifies an item.
type Key struct{ PK, SK string }

// Item is anything storable.
type Item interface {
	Key() (pk, sk string)
	ItemType() string
}

// Expiring items get a DynamoDB TTL. A zero time means the item does not
// expire.
type Expiring interface {
	ExpiresAt() time.Time
}

// Raw is one stored item, decodable into a typed struct.
type Raw map[string]types.AttributeValue

func (r Raw) Key() Key {
	k := Key{}
	if v, ok := r["PK"].(*types.AttributeValueMemberS); ok {
		k.PK = v.Value
	}
	if v, ok := r["SK"].(*types.AttributeValueMemberS); ok {
		k.SK = v.Value
	}
	return k
}

func (r Raw) Decode(out any) error { return attributevalue.UnmarshalMap(r, out) }

// Op is one write in a transaction: put the item, delete the item at
// Delete (whether or not it is there), or, with Check, write nothing and
// only test the condition. Each fails the whole transaction if, with
// IfAbsent, an item with its key already exists or, with IfMatch, the
// stored item's attribute no longer has the value given. Set exactly one
// of Put, Delete and Check.
type Op struct {
	Put      Item
	Delete   *Key
	Check    *Key
	IfAbsent bool
	IfMatch  *Match
}

// encode returns the key of the item the op is about and, for a Put, the
// item encoded.
func (op Op) encode() (Key, Raw, error) {
	switch {
	case op.Put != nil && op.Delete == nil && op.Check == nil:
		r, err := Encode(op.Put)
		if err != nil {
			return Key{}, nil, err
		}
		return r.Key(), r, nil
	case op.Put == nil && op.Delete != nil && op.Check == nil:
		return *op.Delete, nil, nil
	case op.Put == nil && op.Delete == nil && op.Check != nil:
		if !op.IfAbsent && op.IfMatch == nil {
			return Key{}, nil, errors.New("store: a Check op needs a condition")
		}
		return *op.Check, nil, nil
	}
	return Key{}, nil, errors.New("store: an op needs exactly one of Put, Delete and Check")
}

// Match is an attribute and the value it must still have.
type Match struct {
	Attr  string
	Value any
}

type Store interface {
	Get(ctx context.Context, key Key, out any) error
	Put(ctx context.Context, item Item) error
	// PutIfAbsent fails with ErrConflict if the key exists.
	PutIfAbsent(ctx context.Context, item Item) error
	// Delete removes an item; ErrNotFound if it was not there.
	Delete(ctx context.Context, key Key) error
	// Transact applies ops atomically; an op whose condition fails
	// yields ErrConflict and nothing is written.
	Transact(ctx context.Context, ops ...Op) error
}

// Encode marshals an item with its keys, type and TTL.
func Encode(item Item) (Raw, error) {
	m, err := attributevalue.MarshalMap(item)
	if err != nil {
		return nil, err
	}
	pk, sk := item.Key()
	if pk == "" || sk == "" {
		return nil, fmt.Errorf("store: %T has an empty key", item)
	}
	m["PK"] = &types.AttributeValueMemberS{Value: pk}
	m["SK"] = &types.AttributeValueMemberS{Value: sk}
	m["type"] = &types.AttributeValueMemberS{Value: item.ItemType()}
	if ex, ok := item.(Expiring); ok {
		if t := ex.ExpiresAt(); !t.IsZero() {
			m["ttl"] = &types.AttributeValueMemberN{Value: fmt.Sprint(t.Unix())}
		}
	}
	return Raw(m), nil
}

package store

import (
	"context"
	"reflect"
	"sync"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
)

// Memory is an in-memory Store with DynamoDB's semantics for the
// operations Dustjacket uses. Tests use it, and so does `dustjacket
// serve -memory`.
type Memory struct {
	mu    sync.Mutex
	items map[Key]Raw
}

func NewMemory() *Memory { return &Memory{items: map[Key]Raw{}} }

func (m *Memory) Get(_ context.Context, key Key, out any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.items[key]
	if !ok {
		return ErrNotFound
	}
	return r.Decode(out)
}

func (m *Memory) Put(_ context.Context, item Item) error {
	r, err := Encode(item)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[r.Key()] = r
	return nil
}

func (m *Memory) PutIfAbsent(ctx context.Context, item Item) error {
	return m.Transact(ctx, Op{Put: item, IfAbsent: true})
}

func (m *Memory) Delete(_ context.Context, key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[key]; !ok {
		return ErrNotFound
	}
	delete(m.items, key)
	return nil
}

func (m *Memory) Transact(_ context.Context, ops ...Op) error {
	keys := make([]Key, len(ops))
	raws := make([]Raw, len(ops))
	for i, op := range ops {
		k, r, err := op.encode()
		if err != nil {
			return err
		}
		keys[i], raws[i] = k, r
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check everything first, then write: all or nothing.
	for i, op := range ops {
		stored, ok := m.items[keys[i]]
		if ok && op.IfAbsent {
			return ErrConflict
		}
		if op.IfMatch != nil {
			want, err := attributevalue.Marshal(op.IfMatch.Value)
			if err != nil {
				return err
			}
			if !ok || !reflect.DeepEqual(stored[op.IfMatch.Attr], want) {
				return ErrConflict
			}
		}
	}
	for i, op := range ops {
		switch {
		case op.Put != nil:
			m.items[keys[i]] = raws[i]
		case op.Delete != nil:
			delete(m.items, keys[i])
		}
	}
	return nil
}

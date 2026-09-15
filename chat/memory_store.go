package chat

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// errNoThreadValue is the complaint for a Put with nothing to write, which is
// a caller's bug rather than a conflict.
var errNoThreadValue = errors.New("chat: a thread with no id cannot be stored")

// MemoryStore keeps threads in a map. It is the reference implementation of
// [ThreadStore] — what the contract test is written against — and it is what
// a prototype, a test and a single-process CLI actually want: no schema, no
// driver, nothing to configure.
//
// It is safe for concurrent use and copies deeply in both directions, so a
// caller can never reach stored state through a pointer it was handed.
// Nothing survives the process; a server that must outlive a restart swaps in
// a store of its own.
type MemoryStore struct {
	mu      sync.Mutex
	threads map[string]*Thread
}

// NewMemoryStore is an empty store, ready to use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{threads: make(map[string]*Thread)}
}

// Get is the thread with that id, as its own deep copy.
func (m *MemoryStore) Get(ctx context.Context, id string) (*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	thread, ok := m.threads[id]
	if !ok {
		return nil, fmt.Errorf("chat: thread %q: %w", id, ErrThreadNotFound)
	}
	return thread.Clone(), nil
}

// Put writes the thread if its version is the one stored, and reports the new
// version through the value it was given.
func (m *MemoryStore) Put(ctx context.Context, thread *Thread) error {
	if thread == nil || thread.ID == "" {
		return errNoThreadValue
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, exists := m.threads[thread.ID]
	switch {
	case !exists && thread.Version != 0:
		return fmt.Errorf("chat: thread %q does not exist, and the version written was %d rather than 0: %w",
			thread.ID, thread.Version, ErrVersionConflict)
	case exists && stored.Version != thread.Version:
		return fmt.Errorf("chat: thread %q is at version %d, not the %d that was read: %w",
			thread.ID, stored.Version, thread.Version, ErrVersionConflict)
	}

	now := time.Now().UTC()
	thread.Version++
	thread.UpdatedAt = now
	if thread.CreatedAt.IsZero() {
		thread.CreatedAt = now
	}
	m.threads[thread.ID] = thread.Clone()
	return nil
}

// List is every thread, newest first.
func (m *MemoryStore) List(ctx context.Context) ([]*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*Thread, 0, len(m.threads))
	for thread := range maps.Values(m.threads) {
		out = append(out, thread.Clone())
	}
	slices.SortFunc(out, func(a, b *Thread) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out, nil
}

// Delete removes the thread.
func (m *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.threads[id]; !ok {
		return fmt.Errorf("chat: thread %q: %w", id, ErrThreadNotFound)
	}
	delete(m.threads, id)
	return nil
}

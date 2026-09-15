package chat

import (
	"context"
	"errors"
)

// ErrThreadNotFound is what a store answers for an id it does not hold. It is
// a sentinel, so a consumer tests it with errors.Is and a store is free to
// wrap it with the id it was asked for.
var ErrThreadNotFound = errors.New("chat: no thread with that id")

// ErrVersionConflict is what [ThreadStore.Put] answers when the stored thread
// is not at the version the caller read: somebody else wrote in between, so
// the write would silently discard their work. The caller re-reads, reapplies
// what it meant to change, and writes again.
var ErrVersionConflict = errors.New("chat: the thread changed since it was read")

// ThreadStore is where threads live between runs. The built-in
// implementation is [MemoryStore]; a consumer that wants SQLite, Postgres or
// a document database writes its own and asserts the same contract with
// storetest.Run.
//
// Every method takes a context so a store can be a network service, and every
// implementation is safe for concurrent use: one service runs many threads at
// once, and the version check is only meaningful if it is atomic.
type ThreadStore interface {
	// Get is the thread with that id, or ErrThreadNotFound. The thread it
	// returns is the caller's own: writing to it, or to its conversation,
	// never reaches the store.
	Get(ctx context.Context, id string) (*Thread, error)

	// Put writes the thread, checking the version the caller read against
	// the version stored. A thread at version zero that does not exist is
	// created; anything else whose stored version differs is refused with
	// ErrVersionConflict, so two writers racing over one thread leave
	// exactly one winner.
	//
	// On success the store takes its own deep copy and writes the new
	// version and UpdatedAt back into the thread it was given, so the
	// caller may keep writing to the same value; a thread whose CreatedAt
	// is zero is given one as it is created. A refused Put changes
	// nothing, in the store or in the value.
	Put(ctx context.Context, thread *Thread) error

	// List is every thread, newest first: ordered by UpdatedAt descending,
	// and by id descending where two share an UpdatedAt. The threads are
	// complete and are the caller's own, as Get's are.
	List(ctx context.Context) ([]*Thread, error)

	// Delete removes the thread, or answers ErrThreadNotFound. There is no
	// version check: deleting is what a caller does when it no longer
	// cares what the thread holds.
	Delete(ctx context.Context, id string) error
}

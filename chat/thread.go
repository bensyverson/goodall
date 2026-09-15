// Package chat is the optional layer that turns goodall's agent loop into a
// chat back end: threads that own a conversation, a store that persists them,
// and a service that runs the agent against a thread on its own goroutine so a
// client can attach, detach and reattach while the model answers.
//
// The back end owns threads. A front end holds a thread id and sends new
// input; it never holds the system prompt, the tool results or the history,
// because it cannot be trusted with them and should not be made to keep them.
//
// Runs are owned by the service, not by the request that started them. A
// mobile app that backgrounds mid-answer, a browser tab that is closed, a CLI
// that is interrupted — none of them cancel the run. [Service.Send] starts it
// and returns; [Service.Subscribe] attaches to it, replaying what has happened
// so far and then following it live; [Service.Stop] is the only thing that
// ends a run early. When the run ends the service persists the thread, whether
// or not anyone was listening.
//
// The zero-dependency core is in the parent package, [github.com/bensyverson/goodall],
// and everything here is optional: an agent that is not chat-shaped uses
// [goodall.Agent] directly and imports none of this.
package chat

import (
	"time"
	"uuid"

	"github.com/bensyverson/goodall"
)

// Thread is one chat: its history, what it has cost so far, and the version
// that makes concurrent writes safe. It is plain data — every field is
// exported and JSON-serializable — so a store can keep it as a document, a
// row, or anything else that round-trips JSON.
//
// Usage and Cost are the totals over every run the thread has carried, not the
// last one: a chat's ledger is what a consumer bills and what a budget is
// measured against.
type Thread struct {
	// ID is a uuid v7 as a string, so ids sort by age.
	ID string `json:"id"`
	// Conversation is the canonical history, the value a run continues
	// from and the value a run's result replaces it with.
	Conversation goodall.Conversation `json:"conversation"`
	// Usage is the sum of every run's tokens.
	Usage goodall.Usage `json:"usage,omitzero"`
	// Cost is the sum of every run's cost, reported only when every run's
	// was.
	Cost goodall.Cost `json:"cost,omitzero"`
	// Version is the store's optimistic lock: the value a caller read, and
	// what [ThreadStore.Put] checks before it writes. A thread that has
	// never been stored is at version zero.
	Version int `json:"version"`
	// CreatedAt is when the thread was first stored.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when it was last stored.
	UpdatedAt time.Time `json:"updated_at"`
}

// NewThread is an empty thread with a fresh uuid v7 id and its timestamps set.
// It is at version zero, so the first [ThreadStore.Put] creates it.
func NewThread() *Thread {
	now := time.Now().UTC()
	return &Thread{ID: uuid.NewV7().String(), CreatedAt: now, UpdatedAt: now}
}

// Clone is a copy that shares nothing with the original: the conversation is
// rebuilt message by message, so neither value can be changed through the
// other. It is what a store uses to keep a caller from reaching into stored
// state through a pointer it was handed.
func (t *Thread) Clone() *Thread {
	if t == nil {
		return nil
	}
	out := *t
	out.Conversation = goodall.Conversation{}.Append(t.Conversation.Messages()...)
	return &out
}

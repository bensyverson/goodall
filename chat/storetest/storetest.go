// Package storetest is the contract test for [chat.ThreadStore]. A store
// that passes it behaves the way the chat service expects: optimistic
// versioning that admits exactly one of two concurrent writers, copies deep
// enough that a caller cannot reach into stored state, and a listing ordered
// newest first.
//
// It lives in its own package rather than in chat's tests so that a store
// written elsewhere — SQLite, Postgres, Redis — can assert the same contract
// in one line:
//
//	func TestMyStore(t *testing.T) {
//	    storetest.Run(t, func(t *testing.T) chat.ThreadStore { return openMyStore(t) })
//	}
//
// open is called once per subtest and must return an empty store; anything
// the store needs torn down is registered with t.Cleanup by open itself.
package storetest

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
)

// Run asserts that the stores open returns satisfy the ThreadStore contract.
// Each subtest gets its own empty store, so a failure in one leaves the
// others meaningful.
func Run(t *testing.T, open func(t *testing.T) chat.ThreadStore) {
	t.Helper()

	t.Run("GetOfAMissingThread", func(t *testing.T) { testGetMissing(t, open(t)) })
	t.Run("PutCreates", func(t *testing.T) { testPutCreates(t, open(t)) })
	t.Run("PutOfAMissingThreadAtAVersionConflicts", func(t *testing.T) { testPutMissingVersioned(t, open(t)) })
	t.Run("PutAtAStaleVersionConflicts", func(t *testing.T) { testStaleVersion(t, open(t)) })
	t.Run("ConcurrentPutsLeaveOneWinner", func(t *testing.T) { testConcurrentPuts(t, open(t)) })
	t.Run("ListIsNewestFirst", func(t *testing.T) { testListOrder(t, open(t)) })
	t.Run("Delete", func(t *testing.T) { testDelete(t, open(t)) })
	t.Run("TheStoreCopiesDeeply", func(t *testing.T) { testDeepCopy(t, open(t)) })
	t.Run("AStoredThreadRoundTripsThroughJSON", func(t *testing.T) { testJSONRoundTrip(t, open(t)) })
}

// sampleConversation is a history with every awkward shape a thread carries:
// a user turn, an assistant turn with text and a tool call, and the result.
func sampleConversation() goodall.Conversation {
	return goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "say hi"}),
		goodall.AssistantMessage(
			goodall.Text{Text: "calling the tool"},
			goodall.ToolUse{ID: "tu_1", Name: "echo", Input: jsontext.Value(`{"text":"hi"}`)},
		),
		goodall.UserMessage(goodall.ToolResult{ToolUseID: "tu_1", Content: goodall.Blocks{goodall.Text{Text: "echo: hi"}}}),
	)
}

// mustJSON is a thread as JSON, which is how these tests compare two threads:
// every field is exported and serialisable, so the bytes are the value.
func mustJSON(t *testing.T, thread *chat.Thread) string {
	t.Helper()
	b, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("marshalling a thread: %v", err)
	}
	return string(b)
}

// put is a Put that must succeed.
func put(t *testing.T, store chat.ThreadStore, thread *chat.Thread) {
	t.Helper()
	if err := store.Put(t.Context(), thread); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func testGetMissing(t *testing.T, store chat.ThreadStore) {
	got, err := store.Get(t.Context(), "no-such-thread")
	if !errors.Is(err, chat.ErrThreadNotFound) {
		t.Fatalf("Get of a missing thread = %v, want ErrThreadNotFound", err)
	}
	if got != nil {
		t.Errorf("Get of a missing thread returned a thread: %v", got)
	}
}

func testPutCreates(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	thread.Conversation = sampleConversation()
	if thread.Version != 0 {
		t.Fatalf("a new thread is at version %d, want 0", thread.Version)
	}
	put(t, store, thread)
	if thread.Version != 1 {
		t.Errorf("after Put the thread is at version %d, want 1: Put reports the stored version through the value it was given", thread.Version)
	}
	if thread.UpdatedAt.IsZero() {
		t.Error("after Put the thread has no UpdatedAt")
	}

	got, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mustJSON(t, got) != mustJSON(t, thread) {
		t.Errorf("Get returned\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, thread))
	}

	// The same value, carrying the version Put wrote back, updates.
	got.Conversation = got.Conversation.Append(goodall.AssistantMessage(goodall.Text{Text: "again"}))
	put(t, store, got)
	if got.Version != 2 {
		t.Errorf("the second Put left version %d, want 2", got.Version)
	}
	after, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Conversation.Len() != 4 {
		t.Errorf("the stored conversation has %d messages, want 4", after.Conversation.Len())
	}
}

func testPutMissingVersioned(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	thread.Version = 3
	err := store.Put(t.Context(), thread)
	if !errors.Is(err, chat.ErrVersionConflict) {
		t.Fatalf("Put of a thread that does not exist at version 3 = %v, want ErrVersionConflict", err)
	}
	if _, err := store.Get(t.Context(), thread.ID); !errors.Is(err, chat.ErrThreadNotFound) {
		t.Errorf("the refused Put stored the thread anyway: Get = %v", err)
	}
}

func testStaleVersion(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	put(t, store, thread)

	first, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	second, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	first.Conversation = first.Conversation.Append(goodall.UserMessage(goodall.Text{Text: "first"}))
	put(t, store, first)

	second.Conversation = second.Conversation.Append(goodall.UserMessage(goodall.Text{Text: "second"}))
	if err := store.Put(t.Context(), second); !errors.Is(err, chat.ErrVersionConflict) {
		t.Fatalf("the stale Put = %v, want ErrVersionConflict", err)
	}

	got, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("the stored thread is at version %d, want 2: the refused write must not count", got.Version)
	}
	last, _ := got.Conversation.Last()
	if last.Text() != "first" {
		t.Errorf("the stored conversation ends in %q, want the winner's %q", last.Text(), "first")
	}
}

func testConcurrentPuts(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	put(t, store, thread)

	const writers = 8
	read, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		other []error
	)
	start := make(chan struct{})
	for i := range writers {
		// Every writer holds its own copy of the thread as it was read,
		// which is the situation the version check exists for.
		mine := read.Clone()
		mine.Conversation = mine.Conversation.Append(goodall.UserMessage(goodall.Text{Text: string(rune('a' + i))}))
		wg.Go(func() {
			<-start
			err := store.Put(context.Background(), mine)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, chat.ErrVersionConflict):
			default:
				other = append(other, err)
			}
		})
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d of %d concurrent Puts succeeded, want exactly 1", wins, writers)
	}
	for _, err := range other {
		t.Errorf("a losing Put failed with %v, want ErrVersionConflict", err)
	}
	got, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("the stored thread is at version %d, want 2: exactly one writer may have landed", got.Version)
	}
}

func testListOrder(t *testing.T, store chat.ThreadStore) {
	if got, err := store.List(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("List of an empty store = %d threads, %v; want 0, nil", len(got), err)
	}

	var ids []string
	for range 3 {
		thread := chat.NewThread()
		put(t, store, thread)
		ids = append(ids, thread.ID)
	}

	got, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var gotIDs []string
	for _, thread := range got {
		gotIDs = append(gotIDs, thread.ID)
	}
	slices.Reverse(ids)
	if !slices.Equal(gotIDs, ids) {
		t.Errorf("List = %v, want %v (newest first)", gotIDs, ids)
	}
}

func testDelete(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	put(t, store, thread)

	if err := store.Delete(t.Context(), thread.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(t.Context(), thread.ID); !errors.Is(err, chat.ErrThreadNotFound) {
		t.Errorf("Get after Delete = %v, want ErrThreadNotFound", err)
	}
	if err := store.Delete(t.Context(), thread.ID); !errors.Is(err, chat.ErrThreadNotFound) {
		t.Errorf("Delete of a missing thread = %v, want ErrThreadNotFound", err)
	}
}

func testDeepCopy(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	thread.Conversation = sampleConversation()
	put(t, store, thread)
	stored := mustJSON(t, thread)

	// Writing through the value that was handed to Put must not reach the
	// store, and neither must writing through the value Get handed back.
	thread.Conversation = thread.Conversation.Append(goodall.UserMessage(goodall.Text{Text: "sneaky"}))
	thread.Usage.Input = 999

	got, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mustJSON(t, got) != stored {
		t.Errorf("mutating the thread that was Put changed the store:\n%s\nwant\n%s", mustJSON(t, got), stored)
	}

	got.Conversation = got.Conversation.Append(goodall.UserMessage(goodall.Text{Text: "sneakier"}))
	got.Cost.Currency = "XXX"
	again, err := store.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mustJSON(t, again) != stored {
		t.Errorf("mutating the thread that Get returned changed the store:\n%s\nwant\n%s", mustJSON(t, again), stored)
	}
}

func testJSONRoundTrip(t *testing.T, store chat.ThreadStore) {
	thread := chat.NewThread()
	thread.Conversation = sampleConversation()
	thread.Usage = goodall.Usage{Input: 11, Output: 22, CacheRead: 3}
	amount, err := goodall.ParseDecimal("0.0125")
	if err != nil {
		t.Fatalf("parsing a decimal: %v", err)
	}
	thread.Cost = goodall.Cost{Amount: amount, Currency: "USD", Reported: true}
	put(t, store, thread)

	got, gerr := store.Get(t.Context(), thread.ID)
	if gerr != nil {
		t.Fatalf("Get: %v", gerr)
	}
	encoded := mustJSON(t, got)

	var back chat.Thread
	if err := json.Unmarshal([]byte(encoded), &back); err != nil {
		t.Fatalf("unmarshalling a thread: %v", err)
	}
	if again := mustJSON(t, &back); again != encoded {
		t.Errorf("a stored thread did not round-trip:\n%s\nwant\n%s", again, encoded)
	}
}

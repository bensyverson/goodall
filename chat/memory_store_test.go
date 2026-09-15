package chat_test

import (
	"testing"

	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/chat/storetest"
)

// TestMemoryStore is the whole of MemoryStore's test: it is the reference
// implementation, so what it has to do is exactly the store contract.
func TestMemoryStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) chat.ThreadStore {
		return chat.NewMemoryStore()
	})
}

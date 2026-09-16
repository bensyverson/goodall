package chat_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/openrouter"
)

// The chat consumer's quick start: a service over an agent and a store, a
// thread, one message sent and its answer streamed, then the redacted view a
// front end would be handed. It is the README's second example and compiles
// with the suite; it is not run, because it needs a key.
func ExampleService_Send() {
	ctx := context.Background()

	agent := &goodall.Agent{
		Provider: openrouter.New(openrouter.WithAPIKey(os.Getenv("OPENROUTER_API_KEY"))),
		Model:    "anthropic/claude-sonnet-5",
		System:   "You are a friendly assistant.",
	}
	svc := chat.NewService(agent, chat.NewMemoryStore())
	defer svc.Shutdown(ctx)

	thread, err := svc.Create(ctx)
	if err != nil {
		log.Fatal(err)
	}
	run, err := svc.Send(ctx, thread.ID, goodall.Text{Text: "Hello! What can you do?"})
	if err != nil {
		log.Fatal(err)
	}
	for ev, _ := range run.Events {
		if d, ok := ev.(goodall.TextDelta); ok {
			fmt.Print(d.Text)
		}
	}

	view, err := svc.View(ctx, thread.ID) // no system prompt, no tool internals
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d messages\n", len(view.Messages))
}

// An HTTP handler that streams a thread's run to the browser as server-sent
// events, redacted the way the thread's view is: the shape the web example is
// built on.
func ExampleWriteSSE() {
	var svc *chat.Service // built as in ExampleService_Send

	http.HandleFunc("GET /threads/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		if err := chat.WriteSSE(w, chat.Redact(svc.Subscribe(r.Context(), r.PathValue("id")), svc.ViewOptions())); err != nil {
			log.Println(err)
		}
	})
}

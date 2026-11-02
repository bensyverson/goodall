// Command probe-predicate-criteria asks each decision backend a predicate with only
// its True description, only its False description and both, through the production
// clients, and reports which it accepts. decision.Predicate calls each one optional,
// so a refusal on any backend is a call that type allows and that server turns away.
//
// Every run spends real money, a fraction of a cent, except on a local server:
//
//	go run ./scripts/probe-predicate-criteria -env /abs/path/.env -runs 3
//
// The findings it produced are in project/2026-11-02-decision-models-findings.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/conformance"
	"github.com/bensyverson/goodall/internal/dotenv"
)

// callTimeout bounds one call.
const callTimeout = 90 * time.Second

// arm is one way of describing a predicate's two ends.
type arm struct {
	name    string
	yes, no string
}

// arms are the forms compared; "both" is the control, showing that the backend
// answers this question at all.
var arms = []arm{
	{"both", "Money or work is blocked right now", "The sender can wait"},
	{"true_only", "Money or work is blocked right now", ""},
	{"false_only", "", "The sender can wait"},
}

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the API keys")
	runs := flag.Int("runs", 3, "repetitions of each arm on each backend")
	flag.Parse()
	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe-predicate-criteria:", err)
		os.Exit(1)
	}
	for _, backend := range conformance.Backends {
		if backend.KeyEnv != "" && keys[backend.KeyEnv] == "" {
			fmt.Printf("%-11s not asked: no %s in the env file\n", backend.Name, backend.KeyEnv)
			continue
		}
		client := backend.New(keys[backend.KeyEnv], http.DefaultClient)
		for _, a := range arms {
			answered, refusal := 0, ""
			for range *runs {
				ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
				answers, err := client.Decide(ctx, &decision.Request{
					Model: backend.Model,
					State: conformance.Complaint,
					Questions: decision.Questions{{ID: "is_urgent", Question: decision.Predicate{
						Instructions: "Does this ticket convey urgency?",
						True:         a.yes,
						False:        a.no,
					}}},
				})
				cancel()
				if err != nil {
					refusal = err.Error()
					continue
				}
				if _, err := answers.Predicate("is_urgent"); err != nil {
					refusal = err.Error()
					continue
				}
				answered++
			}
			fmt.Printf("%-11s %-11s answered %d of %d %s\n", backend.Name, a.name, answered, *runs, refusal)
		}
	}
}

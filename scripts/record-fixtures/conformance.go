package main

import (
	"context"
	"fmt"

	"github.com/bensyverson/goodall/internal/conformance"
	"github.com/bensyverson/goodall/internal/dotenv"
)

// conformanceExchanges builds one exchange per backend and form of the decision
// conformance suite. Every call is a bare Decide, and the suite's replay holds
// each adapter to the bytes recorded here. A keyless backend is a local server, so
// a plain run needs one up; -only records a single exchange without it.
func conformanceExchanges(rec *recorder, env string) []exchange {
	var out []exchange
	for _, backend := range conformance.Backends {
		for _, form := range conformance.Forms {
			name := backend.Fixture(form)
			out = append(out, exchange{
				name: name,
				what: fmt.Sprintf("the suite's questions, the %s form, on %s", form, backend.Name),
				run: func(ctx context.Context) error {
					key := ""
					if backend.KeyEnv != "" {
						var ok bool
						if key, ok = dotenv.Lookup(env, backend.KeyEnv); !ok {
							return fmt.Errorf("no %s in %s; recording needs a live key, and nothing was written", backend.KeyEnv, env)
						}
					}
					client := backend.New(key, rec.httpClient())
					req := backend.Request(form)
					if form == conformance.FormRefused {
						return recordDecisionRefusal(ctx, rec, client, name, req)
					}
					return askAndSave(ctx, rec, client, name, req.State, req.Questions, req.Model)
				},
			})
		}
	}
	return out
}

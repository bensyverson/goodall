// Command probe-openai-decisions measures the two open questions of OpenAI's
// decision wire, so the rules the openrouter package chose can be checked again.
//
// -mode states posts one predicate by hand over each shape a [decision.Request]
// state can marshal to, and prints what api.openai.com says of it, since the
// documented schema does not say what happens to the shapes it leaves out.
//
// -mode spellings posts candidate foldings of a predicate's true and false
// descriptions into its instructions by hand and prints the input tokens and the
// probability of each, beside the same question with criteria through OpenRouter,
// since a token count is the one thing OpenRouter's response says about what it
// sent upstream.
//
// -mode criteria asks predicates with and without descriptions through
// [openrouter.Client.Decide], the path a consumer uses, on OpenRouter and on OpenAI
// directly, repeated -runs times, and prints the figures per arm.
//
//	go run ./scripts/probe-openai-decisions -env /abs/path/.env -mode states
//	go run ./scripts/probe-openai-decisions -env /abs/path/.env -mode spellings
//	go run ./scripts/probe-openai-decisions -env /abs/path/.env -mode criteria -runs 5
//
// Every call spends real tokens.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/bensyverson/goodall/internal/dotenv"
)

const (
	openAIDecisionsURL = "https://api.openai.com/v1/decisions"
	openAIKeyName      = "OPENAI_API_KEY"
	openRouterKeyName  = "OPENROUTER_API_KEY"
)

// stateProbe is one shape of input to post.
type stateProbe struct {
	name  string
	input string
}

// jsonPart wraps a value as the documented input_json message.
func jsonPart(data string) string {
	return `[{"type":"message","role":"user","content":[{"type":"input_json","data":` + data + `}]}]`
}

// stateProbes is every shape a marshaled state can take, each as the documented
// input_json part and, where the schema might read it, as bare input, and then
// the documented limits.
var stateProbes = []stateProbe{
	{"string_input", `"Help! My payouts have failed and I have staff to pay."`},
	{"json_object", jsonPart(`{"subject":"Payouts failing","body":"Help! I have staff to pay."}`)},
	{"json_array_of_objects", jsonPart(`[{"body":"Help! I have staff to pay."},{"body":"Still waiting."}]`)},
	{"json_empty_object", jsonPart(`{}`)},
	{"json_empty_array", jsonPart(`[]`)},
	{"json_array_of_strings", jsonPart(`["Help! I have staff to pay.","Still waiting."]`)},
	{"json_array_mixed", jsonPart(`[{"body":"Help!"},"Still waiting."]`)},
	{"json_array_of_arrays", jsonPart(`[[{"body":"Help!"}]]`)},
	{"json_string", jsonPart(`"Help! I have staff to pay."`)},
	{"json_number", jsonPart(`42`)},
	{"json_boolean", jsonPart(`true`)},
	{"json_null", jsonPart(`null`)},
	{"bare_number", `42`},
	{"bare_object", `{"body":"Help!"}`},
	{"json_too_deep", jsonPart(strings.Repeat(`{"a":`, 33) + `1` + strings.Repeat(`}`, 33))},
	{"json_too_large", jsonPart(`{"body":"` + strings.Repeat("x", 300*1024) + `"}`)},
}

// statesMode posts each shape named by only, or all of them, and prints the
// status and the body, whatever they are, along with the id header, which the
// documented envelope has no member for.
func statesMode(ctx context.Context, key, only string) error {
	const model, questions = `"gpt-6-luna"`, `[{"type":"predicate","name":"is_urgent","instructions":"Does this convey urgency?"}]`
	for _, probe := range stateProbes {
		if only != "" && !slices.Contains(strings.Split(only, ","), probe.name) {
			continue
		}
		body := `{"model":` + model + `,"input":` + probe.input + `,"questions":` + questions + `}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDecisionsURL, bytes.NewReader([]byte(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("--- %s: %v\n\n", probe.name, err)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		fmt.Printf("--- %s: %d x-request-id=%q\n%s\n\n", probe.name, resp.StatusCode, resp.Header.Get("X-Request-Id"), bytes.TrimSpace(data))
	}
	return nil
}

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the keys")
	mode := flag.String("mode", "states", "states, spellings or criteria")
	runs := flag.Int("runs", 5, "repetitions of each arm, in criteria mode")
	only := flag.String("only", "", "comma-separated shapes to post, in states mode")
	flag.Parse()

	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	switch *mode {
	case "states":
		err = statesMode(ctx, keys[openAIKeyName], *only)
	case "spellings":
		err = spellingsMode(ctx, keys[openAIKeyName], keys[openRouterKeyName])
	case "criteria":
		err = criteriaMode(ctx, keys[openAIKeyName], keys[openRouterKeyName], *runs)
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "probe-openai-decisions:", err)
	os.Exit(1)
}

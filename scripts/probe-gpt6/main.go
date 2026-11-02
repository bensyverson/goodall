// Command probe-gpt6 measures which GPT-6 models can carry a function tool
// alongside reasoning on each endpoint goodall could use.
//
// goodall's OpenAI dialect speaks Chat Completions. internal/livemodel records
// that on 2026-09-15 api.openai.com refused function tools alongside reasoning
// on that endpoint, which would leave a model whose reasoning cannot be turned
// off unable to run as an agent there. This re-measures that claim: one raw
// request per model and effort, on Chat Completions and on Responses, directly
// and through OpenRouter, printing each status, the error's own words, or what
// the model did. A small output cap keeps each call cheap; a truncated answer is
// still an answer that was accepted, which is all this asks.
//
// Every run spends real money, which is why this is a script and not a test:
//
//	go run ./scripts/probe-gpt6 -env /abs/path/.env
//
// The figures it produced are in project/2026-11-02-model-currency-findings.md.
package main

import (
	"context"
	json "encoding/json/v2"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bensyverson/goodall/internal/dotenv"
)

const (
	openAIChat      = "https://api.openai.com/v1/chat/completions"
	openAIResponses = "https://api.openai.com/v1/responses"
	openRouterChat  = "https://openrouter.ai/api/v1/chat/completions"
	callTimeout     = 90 * time.Second
	outputCap       = 400
	prompt          = "What is the weather in Paris? Use the tool."
)

// probe is one model at one effort.
type probe struct {
	model  string
	effort string
}

// probes covers each current GPT-6 model at its lowest documented effort: none
// where the model allows it, low where it does not.
var probes = []probe{
	{"gpt-6-astra", "low"},
	{"gpt-6-astra", "none"},
	{"gpt-6.1-sol", "low"},
	{"gpt-6.1-sol", "none"},
	{"gpt-6.1-terra", "low"},
	{"gpt-6.1-terra", "none"},
	{"gpt-6-luna", "low"},
	{"gpt-6-luna", "none"},
}

// chatTool and responsesTool are one function tool in each endpoint's form.
const (
	toolSchema    = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`
	chatTool      = `{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":` + toolSchema + `}}`
	responsesTool = `{"type":"function","name":"get_weather","description":"Get the weather for a city","parameters":` + toolSchema + `}`
)

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the API keys")
	flag.Parse()
	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe-gpt6:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	for _, p := range probes {
		chat := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"tools":[%s],"reasoning_effort":%q,"max_completion_tokens":%d}`,
			p.model, prompt, chatTool, p.effort, outputCap)
		responses := fmt.Sprintf(`{"model":%q,"input":%q,"tools":[%s],"reasoning":{"effort":%q},"max_output_tokens":%d}`,
			p.model, prompt, responsesTool, p.effort, outputCap)
		router := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"tools":[%s],"reasoning":{"effort":%q},"max_tokens":%d}`,
			"openai/"+p.model, prompt, chatTool, p.effort, outputCap)
		report(ctx, "openai chat", p, openAIChat, keys["OPENAI_API_KEY"], chat)
		report(ctx, "openai responses", p, openAIResponses, keys["OPENAI_API_KEY"], responses)
		report(ctx, "openrouter chat", p, openRouterChat, keys["OPENROUTER_API_KEY"], router)
	}
}

// report makes one call and prints one row.
func report(ctx context.Context, surface string, p probe, url, key, body string) {
	head := fmt.Sprintf("%-17s %-14s effort=%-5s", surface, p.model, p.effort)
	if key == "" {
		fmt.Println(head, "not called: no key in the env file")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		fmt.Println(head, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println(head, err)
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(head, err)
		return
	}
	fmt.Println(head, resp.StatusCode, summarize(data))
}

// summarize reads the fields that say what happened, on either endpoint.
func summarize(data []byte) string {
	var body struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Status  string `json:"status"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Output []struct {
			Type string `json:"type"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return "unreadable body: " + err.Error()
	}
	switch {
	case body.Error != nil:
		return "error: " + body.Error.Message
	case len(body.Choices) > 0:
		c := body.Choices[0]
		return fmt.Sprintf("finish=%s tool_calls=%d", c.FinishReason, len(c.Message.ToolCalls))
	default:
		kinds := make([]string, 0, len(body.Output))
		for _, item := range body.Output {
			kinds = append(kinds, item.Type)
		}
		return fmt.Sprintf("status=%s output=%s", body.Status, strings.Join(kinds, ","))
	}
}

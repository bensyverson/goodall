// Command probe-responses records what the Responses API really does, so the plan
// for goodall's Responses wire rests on observation rather than documentation.
//
// It sends raw requests to api.openai.com/v1/responses and to OpenRouter's
// /responses, and writes each request and each response byte for byte under -out,
// with no headers, so no key can land in a recording. Every call prints one row:
// status, output item types, stream event counts and usage. A refused request is
// an answer here, so a 400 is recorded and the run goes on.
//
// Every run spends real money, which is why this is a script and not a test:
//
//	go run ./scripts/probe-responses -env /abs/path/.env
//	go run ./scripts/probe-responses -env /abs/path/.env -only strip_reasoning
//	go run ./scripts/probe-responses -list
//
// The findings are in project/2026-11-02-responses-wire-plan.md.
package main

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bensyverson/goodall/internal/dotenv"
)

const (
	openAIResponses     = "https://api.openai.com/v1/responses"
	openRouterResponses = "https://openrouter.ai/api/v1/responses"
	includeEncrypted    = "reasoning.encrypted_content"
	callTimeout         = 3 * time.Minute
	outputCap           = 1200
	// prompt makes the model work before it calls the tool. A bare "what is the
	// weather" drew no reasoning item from any GPT-6 model at low effort, which
	// would leave every reasoning round trip below testing nothing.
	prompt       = "Take the sum of the prime numbers below 60, divide it by 11 and round down. If the result is even, get the weather in Paris; if it is odd, get the weather in Lima. Use the tool."
	followUp     = "Thanks. Answer in one word: which city did I ask about?"
	toolResult   = `{"temp_c":18,"sky":"clear"}`
	strictSchema = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`
	looseSchema  = `{"type":"object","properties":{"city":{"type":"string"},"units":{"type":"string"}},"required":["city"]}`
)

// reasoning is the request's reasoning member.
type reasoning struct {
	Effort  string `json:"effort,omitzero"`
	Summary string `json:"summary,omitzero"`
}

// request is one body, built by the exchanges below. Input items are raw because
// replaying a response's items byte for byte is the thing being tested.
type request struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions,omitzero"`
	Input             []jsontext.Value  `json:"input"`
	Tools             []jsontext.Value  `json:"tools,omitzero"`
	Reasoning         *reasoning        `json:"reasoning,omitzero"`
	Include           []string          `json:"include,omitzero"`
	Text              jsontext.Value    `json:"text,omitzero"`
	Metadata          map[string]string `json:"metadata,omitzero"`
	ToolChoice        jsontext.Value    `json:"tool_choice,omitzero"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitzero"`
	Stop              []string          `json:"stop,omitzero"`
	StopSequences     []string          `json:"stop_sequences,omitzero"`
	SafetyIdentifier  string            `json:"safety_identifier,omitzero"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitzero"`
	MaxOutputTokens   int               `json:"max_output_tokens"`
	Store             bool              `json:"store"`
	Stream            bool              `json:"stream,omitzero"`
}

// surface is one server to call.
type surface struct {
	url string
	key string
}

// probe holds what every exchange shares.
type probe struct {
	ctx        context.Context
	out        string
	openAI     surface
	openRouter surface
}

// exchange is one named recording.
type exchange struct {
	name string
	what string
	run  func(p *probe) error
}

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the API keys")
	out := flag.String("out", "scripts/probe-responses/testdata", "where to write the recordings")
	only := flag.String("only", "", "run just this exchange")
	list := flag.Bool("list", false, "print the exchange names and exit")
	flag.Parse()
	all := exchanges()
	if *list {
		for _, e := range all {
			fmt.Printf("%-28s %s\n", e.name, e.what)
		}
		return
	}
	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	p := &probe{
		ctx:        context.Background(),
		out:        *out,
		openAI:     surface{openAIResponses, keys["OPENAI_API_KEY"]},
		openRouter: surface{openRouterResponses, keys["OPENROUTER_API_KEY"]},
	}
	ran := 0
	for _, e := range all {
		if *only != "" && e.name != *only {
			continue
		}
		ran++
		fmt.Printf("== %s: %s\n", e.name, e.what)
		if err := e.run(p); err != nil {
			fmt.Println("   stopped:", err)
		}
	}
	if ran == 0 {
		fatal(fmt.Errorf("no exchange named %q", *only))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "probe-responses:", err)
	os.Exit(1)
}

// exchanges is the table of recordings.
func exchanges() []exchange {
	return append([]exchange{
		{"two_turn_stream_astra", "a streamed tool call and its follow-up, items replayed verbatim", twoTurn("gpt-6-astra", "low", true)},
		{"two_turn_stream_sol", "the same on Sol", twoTurn("gpt-6.1-sol", "low", true)},
		{"two_turn_stream_terra", "the same on Terra", twoTurn("gpt-6.1-terra", "low", true)},
		{"two_turn_stream_luna", "the same on Luna", twoTurn("gpt-6-luna", "low", true)},
		{"two_turn_block_astra", "the same without streaming", twoTurn("gpt-6-astra", "low", false)},
		{"no_include", "does encrypted reasoning arrive when include does not ask?", noInclude},
		{"strip_reasoning", "follow-up with the reasoning item dropped", replayed(dropReasoning)},
		{"strip_encrypted", "follow-up with the reasoning item kept but its encrypted_content removed", replayed(dropEncrypted)},
		{"minimal_call", "follow-up with the function_call rebuilt from call_id, name and arguments alone", replayed(minimalCalls)},
		{"three_turn_minimal", "a third user turn with the assistant's message rebuilt from its text alone", threeTurnMinimal},
		{"parallel", "two tool calls in one turn, answered together", parallel},
		{"effort_max", "reasoning.effort max on each GPT-6 model", efforts("max")},
		{"effort_none", "reasoning.effort none on each GPT-6 model, with a tool", efforts("none")},
		{"incomplete", "an output cap too small to finish, streamed", incomplete},
		{"strict_absent", "a loose schema with no strict member", strictness("strict_absent", "")},
		{"strict_false", "a loose schema with strict false", strictness("strict_false", `,"strict":false`)},
		{"strict_true", "a loose schema with strict true", strictness("strict_true", `,"strict":true`)},
		{"media", "an image and a PDF as input parts", media},
		{"tool_result_image", "an image inside a function_call_output", toolResultImage},
		{"instructions", "the system prompt as the instructions member", instructions},
		{"members", "metadata, safety_identifier, prompt_cache_key and a json_schema text format", members},
		{"unknown_call_id", "a function_call_output answering no call, streamed", unknownCallID},
		{"or_openai", "OpenRouter /responses, a two-turn tool call on GPT-6", routerTwoTurn("openai/gpt-6-astra")},
		{"or_claude", "OpenRouter /responses, a two-turn tool call on Claude", routerTwoTurn("anthropic/claude-sonnet-5.5")},
		{"or_gemini", "OpenRouter /responses, a two-turn tool call on Gemini", routerTwoTurn("google/gemini-4-argon")},
	}, append(choiceExchanges(), replayExchanges()...)...)
}

// userMessage is one user turn as an input item.
func userMessage(text string) jsontext.Value {
	return mustItem(map[string]any{
		"type": "message", "role": "user",
		"content": []map[string]string{{"type": "input_text", "text": text}},
	})
}

func mustItem(v any) jsontext.Value {
	data, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		panic(err)
	}
	return data
}

func weatherTool(schema, extra string) jsontext.Value {
	return jsontext.Value(`{"type":"function","name":"get_weather","description":"Get the weather for a city","parameters":` + schema + extra + `}`)
}

// base is the request every tool exchange starts from.
func base(model, effort string, stream bool) request {
	return request{
		Model:           model,
		Input:           []jsontext.Value{userMessage(prompt)},
		Tools:           []jsontext.Value{weatherTool(strictSchema, `,"strict":true`)},
		Reasoning:       &reasoning{Effort: effort, Summary: "auto"},
		Include:         []string{includeEncrypted},
		MaxOutputTokens: outputCap,
		Stream:          stream,
	}
}

func twoTurn(model, effort string, stream bool) func(*probe) error {
	return func(p *probe) error {
		name := fmt.Sprintf("two_turn_%s_%s", streamWord(stream), model)
		return p.twoTurn(p.openAI, name, base(model, effort, stream))
	}
}

func routerTwoTurn(model string) func(*probe) error {
	return func(p *probe) error {
		name := "or_" + strings.ReplaceAll(model, "/", "_")
		return p.twoTurn(p.openRouter, name, base(model, "low", true))
	}
}

func streamWord(stream bool) string {
	if stream {
		return "stream"
	}
	return "block"
}

func (p *probe) twoTurn(s surface, name string, req request) error {
	output, err := p.call(s, name+".turn1", req)
	if err != nil {
		return err
	}
	req.Input = append(req.Input, output...)
	req.Input = append(req.Input, answers(output)...)
	_, err = p.call(s, name+".turn2", req)
	return err
}

func noInclude(p *probe) error {
	req := base("gpt-6-astra", "low", false)
	req.Include = nil
	_, err := p.call(p.openAI, "no_include", req)
	return err
}

// replayed runs a first turn once and a follow-up with its items transformed.
func replayed(transform func([]jsontext.Value) []jsontext.Value) func(*probe) error {
	return func(p *probe) error {
		name := currentName(transform)
		req := base("gpt-6-astra", "low", false)
		output, err := p.call(p.openAI, name+".turn1", req)
		if err != nil {
			return err
		}
		req.Input = append(req.Input, transform(output)...)
		req.Input = append(req.Input, answers(output)...)
		_, err = p.call(p.openAI, name+".turn2", req)
		return err
	}
}

// currentName tells the three transforms apart by what they do to a sample.
func currentName(transform func([]jsontext.Value) []jsontext.Value) string {
	sample := []jsontext.Value{
		jsontext.Value(`{"type":"reasoning","encrypted_content":"x"}`),
		jsontext.Value(`{"type":"function_call","id":"fc","call_id":"c","name":"n","arguments":"{}"}`),
	}
	out := transform(sample)
	switch {
	case len(out) == 1:
		return "strip_reasoning"
	case !strings.Contains(string(out[0]), "encrypted_content"):
		return "strip_encrypted"
	default:
		return "minimal_call"
	}
}

func itemType(item jsontext.Value) string {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(item, &head); err != nil {
		return "unreadable"
	}
	return head.Type
}

func dropReasoning(items []jsontext.Value) []jsontext.Value {
	return slices.DeleteFunc(slices.Clone(items), func(i jsontext.Value) bool { return itemType(i) == "reasoning" })
}

func dropEncrypted(items []jsontext.Value) []jsontext.Value {
	out := make([]jsontext.Value, 0, len(items))
	for _, item := range items {
		if itemType(item) != "reasoning" {
			out = append(out, item)
			continue
		}
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(item, &fields); err != nil {
			panic(err)
		}
		delete(fields, "encrypted_content")
		out = append(out, mustItem(fields))
	}
	return out
}

func minimalCalls(items []jsontext.Value) []jsontext.Value {
	out := make([]jsontext.Value, 0, len(items))
	for _, item := range items {
		if itemType(item) != "function_call" {
			out = append(out, item)
			continue
		}
		var call struct {
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if err := json.Unmarshal(item, &call); err != nil {
			panic(err)
		}
		out = append(out, mustItem(map[string]string{
			"type": "function_call", "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments,
		}))
	}
	return out
}

func minimalMessages(items []jsontext.Value) []jsontext.Value {
	out := make([]jsontext.Value, 0, len(items))
	for _, item := range items {
		if itemType(item) != "message" {
			out = append(out, item)
			continue
		}
		var msg struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(item, &msg); err != nil {
			panic(err)
		}
		parts := make([]map[string]string, 0, len(msg.Content))
		for _, c := range msg.Content {
			parts = append(parts, map[string]string{"type": "output_text", "text": c.Text})
		}
		out = append(out, mustItem(map[string]any{"type": "message", "role": "assistant", "content": parts}))
	}
	return out
}

func threeTurnMinimal(p *probe) error {
	req := base("gpt-6-astra", "low", false)
	first, err := p.call(p.openAI, "three_turn_minimal.turn1", req)
	if err != nil {
		return err
	}
	req.Input = append(req.Input, minimalCalls(first)...)
	req.Input = append(req.Input, answers(first)...)
	second, err := p.call(p.openAI, "three_turn_minimal.turn2", req)
	if err != nil {
		return err
	}
	req.Input = append(req.Input, minimalMessages(second)...)
	req.Input = append(req.Input, userMessage(followUp))
	_, err = p.call(p.openAI, "three_turn_minimal.turn3", req)
	return err
}

// answers is one function_call_output per function_call, in the model's order.
func answers(items []jsontext.Value) []jsontext.Value {
	var out []jsontext.Value
	for _, item := range items {
		if itemType(item) != "function_call" {
			continue
		}
		var call struct {
			CallID string `json:"call_id"`
		}
		if err := json.Unmarshal(item, &call); err != nil {
			panic(err)
		}
		out = append(out, mustItem(map[string]string{
			"type": "function_call_output", "call_id": call.CallID, "output": toolResult,
		}))
	}
	return out
}

func parallel(p *probe) error {
	req := base("gpt-6-astra", "low", true)
	req.Input = []jsontext.Value{userMessage("What is the weather in Paris and in Lima? Call the tool once per city, in one turn.")}
	return p.twoTurn(p.openAI, "parallel", req)
}

func efforts(effort string) func(*probe) error {
	return func(p *probe) error {
		for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-luna"} {
			if _, err := p.call(p.openAI, "effort_"+effort+"_"+model, base(model, effort, false)); err != nil {
				fmt.Println("   stopped:", err)
			}
		}
		return nil
	}
}

func incomplete(p *probe) error {
	req := base("gpt-6-luna", "low", true)
	req.Tools = nil
	req.Input = []jsontext.Value{userMessage("Write a long poem about tortoises.")}
	req.MaxOutputTokens = 16
	_, err := p.call(p.openAI, "incomplete", req)
	return err
}

func strictness(name, extra string) func(*probe) error {
	return func(p *probe) error {
		req := base("gpt-6-astra", "low", false)
		req.Tools = []jsontext.Value{weatherTool(looseSchema, extra)}
		_, err := p.call(p.openAI, name, req)
		return err
	}
}

func media(p *probe) error {
	pngURL, pdfURL, err := mediaURLs()
	if err != nil {
		return err
	}
	req := base("gpt-6-luna", "low", false)
	req.Tools = nil
	req.Input = []jsontext.Value{mustItem(map[string]any{
		"type": "message", "role": "user",
		"content": []map[string]string{
			{"type": "input_text", "text": "Name the image's color and quote the PDF's text."},
			{"type": "input_image", "image_url": pngURL},
			{"type": "input_file", "filename": "note.pdf", "file_data": pdfURL},
		},
	})}
	_, err = p.call(p.openAI, "media", req)
	return err
}

func toolResultImage(p *probe) error {
	pngURL, _, err := mediaURLs()
	if err != nil {
		return err
	}
	req := base("gpt-6-luna", "low", false)
	first, err := p.call(p.openAI, "tool_result_image.turn1", req)
	if err != nil {
		return err
	}
	req.Input = append(req.Input, first...)
	for _, item := range first {
		if itemType(item) != "function_call" {
			continue
		}
		var call struct {
			CallID string `json:"call_id"`
		}
		if err := json.Unmarshal(item, &call); err != nil {
			return err
		}
		req.Input = append(req.Input, mustItem(map[string]any{
			"type": "function_call_output", "call_id": call.CallID,
			"output": []map[string]string{
				{"type": "input_text", "text": "The tool returned this image. State the exact color of its pixels, and nothing about the weather."},
				{"type": "input_image", "image_url": pngURL},
			},
		}))
	}
	_, err = p.call(p.openAI, "tool_result_image.turn2", req)
	return err
}

func instructions(p *probe) error {
	req := base("gpt-6-luna", "low", false)
	req.Tools = nil
	req.Instructions = "Answer with the single word: tortoise."
	req.Input = []jsontext.Value{userMessage("What is your answer?")}
	_, err := p.call(p.openAI, "instructions", req)
	return err
}

func members(p *probe) error {
	req := base("gpt-6-luna", "low", false)
	req.Tools = nil
	req.Input = []jsontext.Value{userMessage("Give the city of the Eiffel Tower.")}
	req.Metadata = map[string]string{"probe": "responses"}
	req.SafetyIdentifier = "probe-responses"
	req.PromptCacheKey = "probe-responses"
	req.Text = jsontext.Value(`{"format":{"type":"json_schema","name":"city","strict":true,"schema":` + strictSchema + `}}`)
	_, err := p.call(p.openAI, "members", req)
	return err
}

func unknownCallID(p *probe) error {
	req := base("gpt-6-luna", "low", true)
	req.Input = append(req.Input, mustItem(map[string]string{
		"type": "function_call_output", "call_id": "call_nothing", "output": toolResult,
	}))
	_, err := p.call(p.openAI, "unknown_call_id", req)
	return err
}

// call sends one request, records both directions and prints one row. A refusal
// is reported and returned, so a chain stops where its premise did.
func (p *probe) call(s surface, name string, req request) ([]jsontext.Value, error) {
	if s.key == "" {
		return nil, fmt.Errorf("no key in the env file for %s", s.url)
	}
	body, err := json.Marshal(req, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(p.out, name+".request.json"), body, 0o644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(p.ctx, callTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.key)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	ext := ".json"
	if req.Stream && resp.StatusCode == http.StatusOK {
		ext = ".sse"
	}
	if err := os.WriteFile(filepath.Join(p.out, name+".response"+ext), raw, 0o644); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("   %-40s %d %s\n", name, resp.StatusCode, oneLine(raw))
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var summary responseBody
	events := ""
	if req.Stream {
		summary, events = readStream(raw)
	} else if err := json.Unmarshal(raw, &summary); err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}
	fmt.Printf("   %-40s 200 status=%s %s output=[%s] usage=%s\n", name, summary.Status, incompleteReason(summary), strings.Join(kinds(summary.Output), ","), oneLine(summary.Usage))
	if events != "" {
		fmt.Println("     events:", events)
	}
	return summary.Output, nil
}

// responseBody is the part of a response this script reads.
type responseBody struct {
	Status            string           `json:"status"`
	IncompleteDetails jsontext.Value   `json:"incomplete_details"`
	Output            []jsontext.Value `json:"output"`
	Usage             jsontext.Value   `json:"usage"`
}

func incompleteReason(r responseBody) string {
	if len(r.IncompleteDetails) == 0 || string(r.IncompleteDetails) == "null" {
		return ""
	}
	return "incomplete=" + string(r.IncompleteDetails)
}

func kinds(items []jsontext.Value) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, itemType(item))
	}
	return out
}

// readStream returns the terminal response and the event names in first-seen
// order with their counts.
func readStream(raw []byte) (responseBody, string) {
	var last responseBody
	counts := map[string]int{}
	var order []string
	for line := range strings.Lines(string(raw)) {
		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string       `json:"type"`
			Response responseBody `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if counts[event.Type] == 0 {
			order = append(order, event.Type)
		}
		counts[event.Type]++
		if event.Response.Status != "" {
			last = event.Response
		}
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%s×%d", name, counts[name]))
	}
	return last, strings.Join(parts, " ")
}

func oneLine(data []byte) string {
	text := strings.Join(strings.Fields(string(data)), " ")
	const limit = 400
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}

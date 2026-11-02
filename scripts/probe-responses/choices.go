package main

import (
	"encoding/json/jsontext"
	"fmt"
)

// A prompt with no arithmetic draws no reasoning at low effort, so these stay
// cheap. twoCities must be able to draw two calls for parallel_false to mean
// anything, and the parallel exchange shows it does.
const (
	plainWeather = "What is the weather in Paris? Use the tool."
	twoCities    = "What is the weather in Paris and in Lima? Call the tool once per city."
	preamble     = "Before you call the tool, write one sentence saying what you are about to do. Then get the weather in Paris with the tool."
	pdfByURL     = "https://www.w3.org/WAI/ER/tests/xhtml/testfiles/resources/pdf/dummy.pdf"
)

// choiceExchanges are what the build leaf had to record rather than assume: the
// tool_choice forms, parallel_tool_calls, every spelling of a stop sequence, the
// default for reasoning.summary, whether a commentary phase survives being dropped,
// and input_file by URL.
func choiceExchanges() []exchange {
	return []exchange{
		{"tool_choice_required", "tool_choice required, a prompt that does not need the tool", toolChoice("tool_choice_required", `"required"`, "Say hello.")},
		{"tool_choice_none", "tool_choice none, a prompt that wants the tool", toolChoice("tool_choice_none", `"none"`, plainWeather)},
		{"tool_choice_named", "tool_choice naming a function, a prompt that does not need it", toolChoice("tool_choice_named", `{"type":"function","name":"get_weather"}`, "Say hello.")},
		{"parallel_false", "parallel_tool_calls false, a prompt that wants two calls", parallelFalse},
		{"stop_member", "the Chat member stop", stopMember("stop_member", "stop")},
		{"stop_sequences_member", "the Anthropic member stop_sequences", stopMember("stop_sequences_member", "stop_sequences")},
		{"summary_absent", "reasoning with an effort and no summary member, on a prompt that thinks", summaryAbsent},
		{"phase_commentary", "a prompt asking for a sentence before the call, replayed with phase dropped", phaseCommentary},
		{"file_url", "input_file by URL rather than by data URL", fileURL},
	}
}

func toolChoice(name, choice, prompt string) func(*probe) error {
	return func(p *probe) error {
		req := base("gpt-6-luna", "low", false)
		req.Include = nil
		req.Input = []jsontext.Value{userMessage(prompt)}
		req.ToolChoice = jsontext.Value(choice)
		_, err := p.call(p.openAI, name, req)
		return err
	}
}

func parallelFalse(p *probe) error {
	req := base("gpt-6-luna", "low", false)
	req.Include = nil
	req.Input = []jsontext.Value{userMessage(twoCities)}
	serial := false
	req.ParallelToolCalls = &serial
	_, err := p.call(p.openAI, "parallel_false", req)
	return err
}

func stopMember(name, member string) func(*probe) error {
	return func(p *probe) error {
		req := base("gpt-6-luna", "low", false)
		req.Include = nil
		req.Tools = nil
		req.Input = []jsontext.Value{userMessage("Count from one to ten in words.")}
		switch member {
		case "stop":
			req.Stop = []string{"five"}
		default:
			req.StopSequences = []string{"five"}
		}
		_, err := p.call(p.openAI, name, req)
		return err
	}
}

func summaryAbsent(p *probe) error {
	req := base("gpt-6-astra", "low", false)
	req.Include = nil
	req.Reasoning = &reasoning{Effort: "low"}
	_, err := p.call(p.openAI, "summary_absent", req)
	return err
}

// phaseCommentary replays a turn with its message rebuilt from text, so a
// commentary phase, if one appears, is what gets dropped.
func phaseCommentary(p *probe) error {
	req := base("gpt-6-astra", "low", false)
	req.Include = nil
	req.Input = []jsontext.Value{userMessage(preamble)}
	first, err := p.call(p.openAI, "phase_commentary.turn1", req)
	if err != nil {
		return err
	}
	req.Input = append(req.Input, minimalCalls(minimalMessages(first))...)
	req.Input = append(req.Input, answers(first)...)
	_, err = p.call(p.openAI, "phase_commentary.turn2", req)
	return err
}

func fileURL(p *probe) error {
	req := base("gpt-6-luna", "low", false)
	req.Include = nil
	req.Tools = nil
	req.Input = []jsontext.Value{mustItem(map[string]any{
		"type": "message", "role": "user",
		"content": []map[string]string{
			{"type": "input_text", "text": "Quote the PDF's text."},
			{"type": "input_file", "file_url": pdfByURL},
		},
	})}
	_, err := p.call(p.openAI, "file_url", req)
	if err != nil {
		return fmt.Errorf("input_file by URL: %w", err)
	}
	return nil
}

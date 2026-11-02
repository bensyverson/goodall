package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
)

// replayExchanges test which copy of a reasoning item is the one to send back. Its
// encrypted_content differs on output_item.added, on output_item.done and on the
// terminal response, and every two_turn recording replays the terminal's.
func replayExchanges() []exchange {
	return []exchange{
		{"replay_done", "a follow-up replaying the items as output_item.done carried them", replayDone},
		{"replay_added", "a follow-up replaying the reasoning item as output_item.added carried it", replayAdded},
	}
}

func replayDone(p *probe) error {
	req, raw, err := p.streamedFirstTurn("replay_done")
	if err != nil {
		return err
	}
	done := itemsFromEvent(raw, "response.output_item.done")
	req.Input = append(req.Input, done...)
	req.Input = append(req.Input, answers(done)...)
	_, err = p.call(p.openAI, "replay_done.turn2", req)
	return err
}

// replayAdded keeps the function_call from done, since added holds it with empty
// arguments, and takes only the reasoning item from added.
func replayAdded(p *probe) error {
	req, raw, err := p.streamedFirstTurn("replay_added")
	if err != nil {
		return err
	}
	done := itemsFromEvent(raw, "response.output_item.done")
	for _, item := range itemsFromEvent(raw, "response.output_item.added") {
		if itemType(item) == "reasoning" {
			req.Input = append(req.Input, item)
		}
	}
	req.Input = append(req.Input, dropReasoning(done)...)
	req.Input = append(req.Input, answers(done)...)
	_, err = p.call(p.openAI, "replay_added.turn2", req)
	return err
}

// streamedFirstTurn records a first turn and returns its request and raw stream.
func (p *probe) streamedFirstTurn(name string) (request, []byte, error) {
	req := base("gpt-6-astra", "low", true)
	req.Include = nil
	if _, err := p.call(p.openAI, name+".turn1", req); err != nil {
		return req, nil, err
	}
	raw, err := os.ReadFile(filepath.Join(p.out, name+".turn1.response.sse"))
	return req, raw, err
}

// itemsFromEvent reads the item each frame of one stream event type carried.
func itemsFromEvent(raw []byte, event string) []jsontext.Value {
	var items []jsontext.Value
	for line := range strings.Lines(string(raw)) {
		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
		if !ok {
			continue
		}
		var frame struct {
			Type string         `json:"type"`
			Item jsontext.Value `json:"item"`
		}
		if json.Unmarshal([]byte(data), &frame) == nil && frame.Type == event {
			items = append(items, frame.Item)
		}
	}
	return items
}

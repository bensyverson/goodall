package goodall

import (
	"strings"
	"testing"
)

func TestTextResultHoldsOneTextBlock(t *testing.T) {
	res := TextResult("17 degrees")
	if res.IsError {
		t.Error("TextResult set IsError")
	}
	if res.ToolUseID != "" {
		t.Errorf("ToolUseID = %q, want it left to the loop", res.ToolUseID)
	}
	if len(res.Content) != 1 {
		t.Fatalf("Content = %v, want one block", res.Content)
	}
	if got, ok := res.Content[0].(Text); !ok || got.Text != "17 degrees" {
		t.Errorf("Content[0] = %#v, want Text{Text: \"17 degrees\"}", res.Content[0])
	}
}

func TestErrorResultMarksTheResult(t *testing.T) {
	res := ErrorResult("no such city")
	if !res.IsError {
		t.Error("ErrorResult did not set IsError")
	}
	if res.Text() != "no such city" {
		t.Errorf("Text = %q, want %q", res.Text(), "no such city")
	}
}

func TestBlocksResultKeepsBlockOrder(t *testing.T) {
	res := BlocksResult(Text{Text: "here is the map"}, Image{Source: URLSource("https://example.invalid/m.png")})
	if res.IsError {
		t.Error("BlocksResult set IsError")
	}
	if len(res.Content) != 2 {
		t.Fatalf("Content = %v, want two blocks", res.Content)
	}
	if _, ok := res.Content[0].(Text); !ok {
		t.Errorf("Content[0] = %#v, want a Text block", res.Content[0])
	}
	if _, ok := res.Content[1].(Image); !ok {
		t.Errorf("Content[1] = %#v, want an Image block", res.Content[1])
	}
}

func TestBlocksResultWithNoBlocks(t *testing.T) {
	if res := BlocksResult(); len(res.Content) != 0 {
		t.Errorf("Content = %v, want none", res.Content)
	}
}

func TestJSONResultMarshalsIntoOneTextBlock(t *testing.T) {
	type forecast struct {
		City string `json:"city"`
		High int    `json:"high"`
	}
	res, err := JSONResult(forecast{City: "Paris", High: 17})
	if err != nil {
		t.Fatalf("JSONResult: %v", err)
	}
	const want = `{"city":"Paris","high":17}`
	if res.Text() != want {
		t.Errorf("Text = %s, want %s", res.Text(), want)
	}
	if res.IsError {
		t.Error("JSONResult set IsError")
	}
	if len(res.Content) != 1 {
		t.Errorf("Content = %v, want one block", res.Content)
	}
}

func TestJSONResultReportsAValueItCannotMarshal(t *testing.T) {
	res, err := JSONResult(make(chan int))
	if err == nil {
		t.Fatalf("JSONResult = %+v, want an error", res)
	}
	if len(res.Content) != 0 {
		t.Errorf("Content = %v, want no blocks alongside the error", res.Content)
	}
	if !strings.Contains(err.Error(), "goodall") {
		t.Errorf("error = %q, want it to name the package", err)
	}
}

func TestToolResultTextConcatenatesTextBlocks(t *testing.T) {
	res := BlocksResult(
		Text{Text: "one "},
		Image{Source: URLSource("https://example.invalid/m.png")},
		Text{Text: "two"},
	)
	if got := res.Text(); got != "one two" {
		t.Errorf("Text = %q, want %q", got, "one two")
	}
	if got := (ToolResult{}).Text(); got != "" {
		t.Errorf("Text of an empty result = %q, want an empty string", got)
	}
}

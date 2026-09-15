package main

import (
	"context"
	"math/rand/v2"

	"github.com/bensyverson/goodall"
)

// diceInput is what the model sends to the dice tool. The desc tags are what
// the model actually reads, so the limits are stated where the decision is
// made rather than in the tool's description.
type diceInput struct {
	// Sides is how many faces each die has.
	Sides int `json:"sides" desc:"how many faces each die has, from 2 to 100"`
	// Count is how many dice to roll.
	Count int `json:"count" desc:"how many dice to roll, from 1 to 10"`
}

// diceResult is what the tool sends back. A struct rather than a sentence:
// models read JSON well, and the example is the thing a consumer copies.
type diceResult struct {
	// Sides is the die the roll used.
	Sides int `json:"sides"`
	// Rolls is one number per die, in the order they were rolled.
	Rolls []int `json:"rolls"`
	// Total is the sum of the rolls.
	Total int `json:"total"`
}

// Dice limits, applied by the tool rather than trusted from the model.
const (
	maxDiceSides = 100
	maxDiceCount = 10
)

// newDiceTool is the example's one built-in tool. It rolls dice, so it needs
// no network and no key, and it gives the model a reason to call a tool that
// no amount of reasoning can substitute for.
func newDiceTool() (goodall.Tool, error) {
	return goodall.NewTool("roll_dice",
		"Roll dice and return every roll and their total. Use it whenever the conversation needs a random number, because the numbers must come from a real roll rather than from the model.",
		func(ctx context.Context, in diceInput) (goodall.ToolResult, error) {
			if in.Sides < 2 || in.Sides > maxDiceSides {
				return goodall.ErrorResult("sides must be between 2 and 100; call roll_dice again with a value in that range"), nil
			}
			if in.Count < 1 || in.Count > maxDiceCount {
				return goodall.ErrorResult("count must be between 1 and 10; call roll_dice again with a value in that range"), nil
			}
			result := diceResult{Sides: in.Sides, Rolls: make([]int, in.Count)}
			for i := range result.Rolls {
				result.Rolls[i] = rand.IntN(in.Sides) + 1
				result.Total += result.Rolls[i]
			}
			return goodall.JSONResult(result)
		})
}

package main

import "testing"

// A row is keyed by wire, model and effort, so a pin that lands on a model already
// listed would spend twice and report once.
func TestEveryLoopTargetIsItsOwnRow(t *testing.T) {
	seen := map[loopTarget]bool{}
	for _, target := range loopTargets {
		if seen[target] {
			t.Errorf("%+v is listed twice", target)
		}
		seen[target] = true
	}
}

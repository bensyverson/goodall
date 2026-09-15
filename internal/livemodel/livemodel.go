// Package livemodel names the model each provider's live tests and fixture
// recordings run against.
//
// It exists so the recorder and the live tests cannot drift: a fixture
// recorded from one model and a live test asserting against another would
// disagree about thinking style, capabilities and stop reasons, and the
// disagreement would read as a bug in the provider. One constant per
// provider, changed in one place when the newest model changes.
//
// It is internal because the choice is a test detail, not an API: a consumer
// names their own model on goodall.Request.
package livemodel

// Anthropic is the model the Anthropic live tests and
// scripts/record-fixtures use by default.
//
// It is claude-sonnet-5 because that is the newest model the live catalogue
// confirms: GET /v1/models/claude-sonnet-5 on 2026-09-15 answered "Claude
// Sonnet 5", adaptive thinking, the effort ladder from low to max, a
// 1,000,000-token context and a 128,000-token output limit
// (TestLiveModelReportsImageAndPDFSupport makes the call and logs it). It is
// also the style the provider prefers to send: a budget-only model such as
// claude-sonnet-4-5-20250929 has no wire slot for the thinking display
// setting, so a recording from one would not exercise the adaptive path at
// all. Override it on the recorder with -model.
const Anthropic = "claude-sonnet-5"

// Package goodall is a small, dependency-free core for building agents in Go:
// a conversation of typed content blocks, a provider seam that streams
// events, tools defined from ordinary Go types, and an agent loop that runs
// them to a terminal result.
//
// The root package holds everything a provider or a consumer types against
// and imports only the standard library. Providers live in subpackages
// (anthropic, openrouter), as do the optional chat components (chat). The
// design, its invariants and the reasons behind them are recorded in the
// dated documents under project/.
package goodall

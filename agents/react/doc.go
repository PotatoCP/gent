// Package react implements a native tool agent loop.
//
// NewAgent sends the current ToolChain definitions on each request. A complete model response
// either selects native tools or supplies a final answer to the configured Output contract.
// Tool calls take priority over text in the same response: their results must be observed before
// the agent can finish. Incomplete calls and answers produce retry feedback without tool effects.
//
// TextFormat renders tool results. Task.Instructions supplies system instructions.
// Hooks.BeforeModelCall changes effective requests; Hooks.BeforeToolCall changes effective typed
// input. Gent freezes those values and saves tool input before the external action starts.
//
// Registry, SearchChain, and JsToolChainWrapper share the native ToolChain contract. Search selects
// definitions from active run history. JavaScript is exposed as run_code and uses the same tool
// validation, hooks, limits, and checkpoints for inner calls.
//
// Restore binds current code and configuration where accepted state remains usable.
// Recovery reuses completed results. Uncertain outcomes produce guidance for a new model plan.
// Interrupted JavaScript returns partial evidence without restarting the script.
//
// Recovery can repeat work whose result was not accepted, including model calls that can return
// different content. Agent restores its saved response and tool-plan progress internally; callers
// do not need a custom step enum. Accepted tool results keep their operation identities and are
// reused instead of executing those operations again.
//
// Custom Output.Process implementations, validators, and hooks can run again after interruption.
// Keep external effects in these callbacks safe to repeat. For example, a database write inside
// Output.Process can repeat if execution stops after the write but before the terminal output is
// durably accepted. Managed tool recovery does not protect external effects in these callbacks.
// Use managed tools when an action needs the tool recovery contract. A durably accepted terminal
// output is restored without calling Output.Process again.
package react

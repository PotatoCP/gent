package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// IndexableTool provides metadata for tool search indexing.
//
// Tool embeds this metadata view. DefineTool supplies empty defaults and builder methods for
// each value. Search engines consume this view without needing a tool's generic input/output.
//
// # Example
//
//	type MyTool struct{}
//
//	func (t *MyTool) Name() string        { return "lookup_order" }
//	func (t *MyTool) Description() string { return "Look up order details" }
//	func (t *MyTool) Domain() string      { return "Orders" }
//	func (t *MyTool) Categories() []string { return []string{"lookup", "orders"} }
//	func (t *MyTool) Keywords() []string  { return []string{"order", "tracking"} }
//	func (t *MyTool) SyntheticQueries() []string {
//	    return []string{"find order by ID", "check order status"}
//	}
type IndexableTool = core.IndexableTool

// ToolSearcher indexes tools and searches them by query.
//
// Search engines are pluggable backends for toolchain.SearchChain.
// Each engine provides a different search strategy (e.g., BM25
// full-text search, regex pattern matching).
//
// # Contract
//
//   - Search returns ALL matching tool names ranked by relevance.
//     SearchChain handles pagination over results.
//   - Expected query failures use Status and authored Instructions.
//   - Unexpected Go errors remain private and use safe execution steering.
type ToolSearcher = core.ToolSearcher

// ToolSearchResult contains ranked names and authored model guidance. Executed with no names
// means no matches. InvalidArguments can explain query syntax that JSON Schema cannot check.
// Names are meaningful only for Executed; raw backend errors never become model guidance.
type ToolSearchResult = core.ToolSearchResult

// ToolSearchRequest selects a local engine and an optional page. Page zero selects page one.
type ToolSearchRequest = core.ToolSearchRequest

// ToolExecuteRequest selects one shared execution target. Gent validates that target's schema.
type ToolExecuteRequest = core.ToolExecuteRequest

// SearchableToolChain supplies shared local discovery independently of its search engine.
// The runtime catalog comes from current bindings, never from historical model definitions.
type SearchableToolChain = core.SearchableToolChain

// ToolSearchDefinition is a consistent read-only view of discoverable definitions and pins.
// LocalSearch describes the shared search function. Definitions do not register executable code.
type ToolSearchDefinition = core.ToolSearchDefinition

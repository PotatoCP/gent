A small registry advertises each tool directly. SearchChain exposes shared local search, shared
execution, and pinned definitions. Search replies describe target schemas without promoting every
result into a native definition.

## Configure search

`tool` is an existing typed tool. Add search metadata while building it: Domain, Categories,
Keywords, and SyntheticQueries.

{{example:tools-search}}

Initialize after registering the tools and engines. `Pin` keeps selected definitions advertised.
Page size limits each search reply. The current bindings determine what can run after restoration; a
saved definition does not register executable code.

## Give the search chain to the Runner

After Initialize succeeds:

{{example:discovery-runner}}

Use this Runner for task execution as in [Hello World](/start/hello-world/). Its model can request
tool discovery and then call a selected tool through shared execution.

## Select an engine

Bleve provides keyword search. Regex search finds names by a pattern. `NewFusedToolSearcher` adds
vector search when an embedder is available. See [Local embeddings](/search/embeddings/).

A custom ToolSearcher indexes tool metadata and returns all matching names in ranked order.
SearchChain owns result pagination. Expected query failures use typed status and safe Instructions;
unexpected backend errors remain private diagnostic data.

## Add searchable metadata

{{example:tools-metadata}}

Before Initialize:

{{example:tools-pin}}

## Use an index as an engine

With an existing embedder:

{{example:tools-custom-search}}

For combined keyword and vector search:

{{example:tools-fused-search}}

Register either `engine` before Initialize. This creates a separate chain from the first example:

{{example:search-engine-register}}

Give this `chain` to Config.ToolChain with the Runner setup shown above.

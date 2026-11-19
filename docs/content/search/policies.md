A policy is a short document with a stable ID, readable content, and optional search metadata. Split
unrelated rules into separate documents so a query can retrieve the relevant text.

## Register search and lookup

Use the existing `ctx` and `embedder`:

{{example:policies-register}}

Full content is the default result mode. Snippet mode supports a search-then-read flow. Title mode
returns IDs for discovery. GetPolicyTool reads one document by its exact ID.

## Enforce actions in code

The example policy text is demonstration data, not an application rule supplied by Gent. Retrieval
gives the model guidance. It does not authorize an action. Enforce approval, account access, and
domain conditions in tool handlers before external work.

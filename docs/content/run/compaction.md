Compaction changes the context selected for future model calls. It does not erase the original
history from stored batches. System instructions stay outside compaction.

## Summarize older context

{{example:compaction-summary}}

The generic summarizer makes a normal LLM call. Recent and pinned non-user entries can remain in
context. Native provider compaction is not part of this feature.
The summarizer receives the current system prefix as read-only context. Text includes its role;
structured data and media use attributed native attachments, including cache wrappers. The original
prefix stays unchanged. Saved request evidence shows the content sent through the model adapter.

## Retain user input

{{example:compaction-retention}}

The allowance measures content bytes, not model tokens. Zero selects the default 32 KiB allowance.
Saved origin determines eligibility. Generated corrections, tool guidance, and summaries do not
become user input merely because they have a user role.

A typical change is:

| Before | After |
| --- | --- |
| System prefix | The same prefix |
| Original user messages | Retained newer messages, with an omission notice when needed |
| Older work | Summary of the covered work |
| Recent or required work | Retained context needed by the next call |

Boundary truncation uses more of the allowance than dropping a complete message. It can retain only
part of an instruction. Older explicit input can leave active context; its meaning then depends on
the summary. No original prompt has permanent protection.

Retention runs only at compaction boundaries. Requests can grow by appending messages between those
boundaries. This can help provider cache reuse, but it does not guarantee a hit.

## Use a sliding window

{{example:compaction-window}}

A window drops older active entries without creating an LLM summary. It preserves required contracts
and the original archive. Choose the strategy according to the context your loop needs.

## Attach the selected configuration

Use the `config` from either strategy above. Here `model` is the main loop's model:

{{example:compaction-runner}}

The Runner applies the trigger and strategy during execution. Complete all settings before creating
or restoring a context. Context creation and NewTurn are the same as in
[Hello World](/start/hello-world/).

## Supply a trigger

{{example:compaction-trigger}}

Select it on `config` before creating the Runner:

{{example:compaction-trigger-use}}

The value 4 makes a small example compact quickly. Select a larger allowance for your workload.

A trigger with mutable progress must save that progress through the required state contract. Do not
keep per-run progress in an unsaved shared strategy instance.

## Replace retention

This strategy omits explicit user messages and retains the omission notice:

{{example:retention-custom}}

Set it on the summarization `strategy` from the first example, before creating the Runner:

{{example:compaction-retention-use}}

Retain receives read-only input and proposes data.
It must not write history, reset counters, call models, or perform external work. The standard
compactor accepts the proposal and summary together. A custom `CompactionStrategy.Compact` similarly
returns a `HistoryUpdate`; it must preserve source identities and required native groups.

## Supply a strategy

This implementation delegates selection to the standard window strategy:

{{example:compaction-custom}}

Select it on the same `config`:

{{example:compaction-strategy-use}}

Use this signature for a custom selector. Return a proposal instead of changing history directly.

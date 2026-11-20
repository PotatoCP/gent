A `Task` supplies new conversation input. `Messages` must contain at least one user message. System
messages belong in `Instructions`, not in `Messages`.

## Create one user message

{{example:task-text}}

## Submit the task

Use the `runner` and `execCtx` from [Hello World](/start/hello-world/):

{{example:task-submit}}

The task variants below use this same call. Substitute `withInstructions`, `keep`, or `clear` for
`task`. Submit the next task after the preceding execution call returns.

## Supply system instructions

{{example:task-instructions}}

Gent fixes task input when it admits the turn. The first user prompt and later prompts enter
compactable history. System instructions stay outside compaction.

## Keep or clear the prefix

{{example:task-prefix}}

A non-nil populated `Instructions` slice replaces the saved prefix. This can reduce provider cache
reuse. A cache hit is not guaranteed. Later model calls use the saved prefix and selected history;
they do not append the whole Task again on each iteration.

## Include output guidance

An output contract can supply optional instructions. The caller decides whether to include them.
Here `out` is an existing `gent.Output[string]`.

{{example:output-instructions}}

[Hooks](/run/hooks/) can change effective requests. They cannot change the accepted Task.

The ReAct loop calls a model, handles requested tools, and validates answer candidates. Models and
loop policy belong on the loop. Shared execution services belong in `gent.Config`.

## Set call options

{{example:react-options}}

Token and sampling controls must be supported by the selected provider and model. Set temperature or
top-p only when appropriate for that model. `DefaultSamplingParams` supplies Gent's model-aware
sampling defaults; it does not prove remote support.

## Change result formatting

{{example:react-format}}

The XML formatter is also available. Formatting renders tool-result sections. It does not create
system instructions, parse final-answer tags, or change native tool identities.

## Bound oversized or repeated output

{{example:react-guards}}

ReAct saves prepared decisions and tool-plan progress for recovery. You do not need a custom step
enum to resume this loop. See [Custom loops](/extend/loops/) when you need another control policy.

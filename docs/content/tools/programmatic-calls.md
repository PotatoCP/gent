The JavaScript wrapper exposes `run_code` through the normal tool interface. Inner calls use the
wrapped chain's validation, limits, events, and recovery.

## Wrap the chain

{{example:tools-javascript}}

Give the wrapper to the Runner:

{{example:programmatic-runner}}

The wrapped `chain` must already contain the tools that scripts can call. Use this Runner for the
context and NewTurn calls shown in [Hello World](/start/hello-world/).

BigInt preserves captured integer values in JavaScript. The default number mode can round values
outside JavaScript's safe integer range. The saved Go evidence keeps its original numeric type.

## Call a tool and forward media

This is the script inside a `run_code` request. The registered `receipt` tool returns an image. Only
the selected byte parts are forwarded to the model.

{{example:tools-script}}

## Set execution rules

Set code guidance, direct-call guidance, or WithDirectCallDisabled before use. The wrapper has a
script timeout. It is not a general-purpose security boundary for arbitrary external code. Parallel
calls require explicit support from the wrapped chain.

## Keep effect evidence

Each inner effect has its own saved identity and result. An outer script record alone is not enough
for recovery. An interrupted script reports partial evidence without automatically restarting. The
model can choose later actions from that evidence.

`tool.forward` selects original content from the same script. This permits media delivery without
flattening bytes into text. Its references do not survive as arbitrary pointers across scripts. See
the [programmatic test example](/examples/#tools-and-search) for verified script calls.

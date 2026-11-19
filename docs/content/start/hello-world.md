Create a model, give it to the ReAct loop, and run a task. Set `OPENAI_API_KEY` and `GENT_MODEL` as
shown in [Setup](/start/setup/).

{{example:hello-world}}

`react.NewAgent` uses text output, so `answer` is a string. `NewTurn` submits the task and executes
the loop before it returns. Check `result.Status` and `result.TerminationReason` before using the
answer as completed output.

Keep the Runner and execution context when later tasks belong to the same conversation. Release the
handle when its owner is done. See [Conversations](/run/conversations/) for later turns.

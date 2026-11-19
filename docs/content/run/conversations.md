One execution context represents one conversation. Keep its handle between completed turns. Each
accepted task has a TurnId. Each execution attempt has an AttemptId. The RunId stays the same.

| Identity | Example |
| --- | --- |
| RunId | One conversation with two user tasks |
| TurnId | First task, then a new identity for the second task |
| AttemptId | A new worker attempt after restore or Continue |

## Submit another task

Use an existing `runner` and `execCtx`. Handle the first result before submitting another task.

{{example:conversation-turns}}

NewTurn keeps accepted history, run counters, run data, and the last completed result. It resets
turn baselines, gauges, and declared turn fields. It does not automatically compact the
conversation.

## Read the completed result

{{example:conversation-result}}

## Continue or replace unfinished work

Use [Continue](/storage/restore/#continue-the-task) to resume the accepted task without adding its
prompt again. NewTurn on an idle unfinished turn prepares required recovery, then cancels the old
turn in the same save that admits the new task. An active attempt returns Busy and admits no task.

Live steering while an attempt runs is outside this interface. Do not use another NewTurn call as an
implicit steering channel. Release the context when its owner no longer needs the handle.

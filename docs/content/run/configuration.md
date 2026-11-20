Configure the loop and `gent.Config` before creating or restoring a context. Treat configuration and
reachable values as read-only while in use. Progress belongs to the execution context.

## Select limits

{{example:config-limits}}

An empty Config does not disable recording bounds, subscriber bounds, or Go context cancellation.
Exact iteration limits use `>=` at completed iteration boundaries. Other limits use `>` on updates.

## Limit the complete conversation

{{example:config-run-limit}}

## Give the configuration to the Runner

Select one configuration before creating a context. This example uses `standard`; substitute
`unlimited`, `oneRule`, or `runLimit` to use another choice:

{{example:config-runner}}

Later examples also create or update a Config. Pass that value in place of `standard`. Complete
configuration before creating the Runner, then create a context and submit a task as shown in
[Hello World](/start/hello-world/).

LimitTurn is the default. LimitRun measures cumulative counters. Gauges always describe the current
turn. Continue does not reset an exhausted allowance. Restore with changed configuration if your
application chooses to permit more work.

## Read accounting

{{example:stats-read}}

## Add a counter

{{example:stats-custom}}

Counters only increase. NewTurn establishes new turn baselines and resets gauges. Gent rebuilds
context-size gauges from retained state. Prefix rules apply to each matching key, not an implicit
sum of all matching keys.

## Common services

| Config field | Read next |
| --- | --- |
| ToolChain | [Tools](/agent/tools/) |
| Recorder, Recording | [Save batches](/storage/save-batches/) |
| Codecs | [Codecs](/storage/codecs/) |
| Subscribers | [Subscribers](/observe/subscribers/) |
| Hooks | [Hooks](/run/hooks/) |
| CompactionTrigger, CompactionStrategy | [Compaction](/run/compaction/) |
| NewLoopData | [LoopData](/extend/loop-data/) |

`SteeringMessages` replaces authored guidance where a field is set. Empty fields use defaults. These
strings are ordinary model input. Words such as FULL STOP are not execution commands.

## Set a counter prefix limit

On a Config before use:

{{example:config-prefix-limit}}

## Change default guidance

{{example:config-guidance}}

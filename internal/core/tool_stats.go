package gent

// Only newly published observations reach this path. Replay reuses the saved runtime and does
// not publish old tool events. Recovery outcomes never reset fresh-call error gauges.
func (execCtx *ExecutionContext) updateToolRecordStats(record *ToolCallRecord) {
	name := StatKey(record.Name())
	switch record.Kind() {
	case ToolResponseRecoveryError:
		execCtx.stats.incrCounterDirect(SCToolRecoveryErrorTotal, 1)
		execCtx.stats.incrCounterDirect(SCToolRecoveryErrorFor+name, 1)
	case ToolResponseRecovery:
		return
	case ToolResponseCallError:
		execCtx.addToolError(record.Name())
	case ToolResponseExecution:
		if record.Execution().Status == ToolInvalidArguments {
			execCtx.stats.incrCounterDirect(SCToolCallsInvalidArgumentsTotal, 1)
			execCtx.stats.incrCounterDirect(SCToolCallsInvalidArgumentsFor+name, 1)
			return
		}
		execCtx.stats.ResetGauge(SGToolCallsErrorConsecutive)
		execCtx.stats.ResetGauge(SGToolCallsErrorConsecutiveFor + name)
	}
}

func (execCtx *ExecutionContext) addToolError(name string) {
	execCtx.stats.incrCounterDirect(SCToolCallsErrorTotal, 1)
	execCtx.stats.incrGaugeInternal(SGToolCallsErrorConsecutive, 1)
	if name == "" {
		return
	}
	execCtx.stats.incrCounterDirect(SCToolCallsErrorFor+StatKey(name), 1)
	execCtx.stats.incrGaugeInternal(SGToolCallsErrorConsecutiveFor+StatKey(name), 1)
}

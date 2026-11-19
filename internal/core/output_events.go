package gent

const EventNameOutputValidation = "gent:output:validation"

// OutputValidationStage separates a built-in check, a custom callback, and the loop's final
// processing decision. Only the processing stage counts a candidate once for overall limits.
type OutputValidationStage string

const (
	// OutputValidationLocal records a built-in output format check.
	OutputValidationLocal OutputValidationStage = "local"
	// OutputValidationCustom records the configured application validator decision.
	OutputValidationCustom OutputValidationStage = "custom"
	// OutputValidationProcess records the overall candidate decision and updates rejection limits.
	OutputValidationProcess OutputValidationStage = "process"
)

// OutputValidationEvent records the decision without copying the complete typed value. Steering
// belongs to a rejected candidate. Error means processing failed and does not count as rejection.
type OutputValidationEvent struct {
	BaseEvent
	Stage    OutputValidationStage
	Name     string
	Accepted bool
	Steering []ContentPart
	Error    error
}

const (
	// SCOutputRejectedTotal counts candidates rejected by the complete output-processing decision.
	SCOutputRejectedTotal StatKey = "gent:output_rejected_total"
	// SGOutputRejectedConsecutive measures rejected candidates since the last accepted candidate.
	SGOutputRejectedConsecutive StatKey = "gent:output_rejected_consecutive"
	// SCOutputLocalRejectedTotal counts failures of built-in output validation.
	SCOutputLocalRejectedTotal StatKey = "gent:output_local_rejected_total"
	// SGOutputLocalRejectedConsecutive measures consecutive built-in validation rejections.
	SGOutputLocalRejectedConsecutive StatKey = "gent:output_local_rejected_consecutive"
	// SCOutputCustomRejectedTotal counts application output-validator rejections.
	SCOutputCustomRejectedTotal StatKey = "gent:output_custom_rejected_total"
	// SCOutputCustomRejectedBy prefixes the custom rejection counter with the validator name.
	SCOutputCustomRejectedBy StatKey = "gent:output_custom_rejected_by:"
	// SGOutputCustomRejectedConsecutive measures consecutive application-validator rejections.
	SGOutputCustomRejectedConsecutive StatKey = "gent:output_custom_rejected_consecutive"
)

// PublishOutputValidation queues the decision with its steering. The loop keeps a rejected
// candidate and feedback before it accepts the next checkpoint, including when a limit stops it.
func (execCtx *ExecutionContext) PublishOutputValidation(
	stage OutputValidationStage, name string, accepted bool, steering []ContentPart, err error,
) {
	execCtx.publish(&OutputValidationEvent{
		BaseEvent: BaseEvent{EventName: EventNameOutputValidation}, Stage: stage, Name: name,
		Accepted: accepted, Steering: CloneContentParts(steering), Error: err,
	})
}

// updateOutputStats runs under the same context lock as the other event statistics.
func (execCtx *ExecutionContext) updateOutputStats(event *OutputValidationEvent) {
	if event.Error != nil {
		return
	}
	var total, consecutive StatKey
	switch event.Stage {
	case OutputValidationProcess:
		total, consecutive = SCOutputRejectedTotal, SGOutputRejectedConsecutive
	case OutputValidationLocal:
		total, consecutive = SCOutputLocalRejectedTotal, SGOutputLocalRejectedConsecutive
	case OutputValidationCustom:
		total, consecutive = SCOutputCustomRejectedTotal, SGOutputCustomRejectedConsecutive
	default:
		return
	}
	if event.Accepted {
		execCtx.stats.ResetGauge(consecutive)
		return
	}
	execCtx.stats.incrCounterDirect(total, 1)
	execCtx.stats.incrGaugeInternal(consecutive, 1)
	if event.Stage == OutputValidationCustom {
		execCtx.stats.incrCounterDirect(SCOutputCustomRejectedBy+StatKey(event.Name), 1)
	}
}

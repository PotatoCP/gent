package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

const EventNameOutputValidation = core.EventNameOutputValidation

// OutputValidationStage separates a built-in check, a custom callback, and the loop's final
// processing decision. Only the processing stage counts a candidate once for overall limits.
type OutputValidationStage = core.OutputValidationStage

// OutputValidationLocal records a built-in output format check.
const OutputValidationLocal OutputValidationStage = core.OutputValidationLocal

// OutputValidationCustom records the configured application validator decision.
const OutputValidationCustom OutputValidationStage = core.OutputValidationCustom

// OutputValidationProcess records the overall candidate decision and updates rejection limits.
const OutputValidationProcess OutputValidationStage = core.OutputValidationProcess

// OutputValidationEvent records the decision without copying the complete typed value. Steering
// belongs to a rejected candidate. Error means processing failed and does not count as rejection.
type OutputValidationEvent = core.OutputValidationEvent

// SCOutputRejectedTotal counts candidates rejected by the complete output-processing decision.
const SCOutputRejectedTotal StatKey = core.SCOutputRejectedTotal

// SGOutputRejectedConsecutive measures rejected candidates since the last accepted candidate.
const SGOutputRejectedConsecutive StatKey = core.SGOutputRejectedConsecutive

// SCOutputLocalRejectedTotal counts failures of built-in output validation.
const SCOutputLocalRejectedTotal StatKey = core.SCOutputLocalRejectedTotal

// SGOutputLocalRejectedConsecutive measures consecutive built-in validation rejections.
const SGOutputLocalRejectedConsecutive StatKey = core.SGOutputLocalRejectedConsecutive

// SCOutputCustomRejectedTotal counts application output-validator rejections.
const SCOutputCustomRejectedTotal StatKey = core.SCOutputCustomRejectedTotal

// SCOutputCustomRejectedBy prefixes the custom rejection counter with the validator name.
const SCOutputCustomRejectedBy StatKey = core.SCOutputCustomRejectedBy

// SGOutputCustomRejectedConsecutive measures consecutive application-validator rejections.
const SGOutputCustomRejectedConsecutive StatKey = core.SGOutputCustomRejectedConsecutive

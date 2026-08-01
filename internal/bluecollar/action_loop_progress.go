package bluecollar

import "strings"

type actionProgressEvaluation struct {
	HasProgress                      bool
	Reason                           string
	ProgressEventCount               int
	ConsecutiveNoProgressActionCount int
}

type actionProgressTracker struct {
	lastProgressEventCount           int
	consecutiveNoProgressActionCount int
	stallRecoveryDirectiveCount      int
}

type recoveryAllowance struct {
	CanRecover bool   `json:"canRecover"`
	Reason     string `json:"reason"`
}

func newActionProgressTracker(observations []turnObservation) actionProgressTracker {
	return actionProgressTracker{
		lastProgressEventCount: progressEventCount(observations),
	}
}

func (tracker *actionProgressTracker) evaluate(observations []turnObservation) actionProgressEvaluation {
	currentProgressEventCount := progressEventCount(observations)
	if currentProgressEventCount > tracker.lastProgressEventCount {
		tracker.lastProgressEventCount = currentProgressEventCount
		tracker.consecutiveNoProgressActionCount = 0
		tracker.stallRecoveryDirectiveCount = 0
		return actionProgressEvaluation{
			HasProgress:        true,
			Reason:             "new progress event recorded",
			ProgressEventCount: currentProgressEventCount,
		}
	}
	tracker.consecutiveNoProgressActionCount++
	return actionProgressEvaluation{
		HasProgress:                      false,
		Reason:                           "no new workspace, tool, artifact, attachment, or failure progress event",
		ProgressEventCount:               currentProgressEventCount,
		ConsecutiveNoProgressActionCount: tracker.consecutiveNoProgressActionCount,
	}
}

func (evaluation actionProgressEvaluation) shouldStop() bool {
	return !evaluation.HasProgress && evaluation.ConsecutiveNoProgressActionCount >= 3
}

func (tracker *actionProgressTracker) noteStallRecoveryDirective(observations []turnObservation) {
	tracker.lastProgressEventCount = progressEventCount(observations)
	tracker.consecutiveNoProgressActionCount = 0
	tracker.stallRecoveryDirectiveCount++
}

// structuralFailureRepeatThreshold is how many times the same failure signature
// (tool + stage + error code) may recur — across cosmetically different inputs —
// before recovery treats that route as structurally broken and stops. Below this
// the normal corrected-retry / alternate-route budget still applies.
const structuralFailureRepeatThreshold = 3

func evaluateRecoveryAllowance(observations []turnObservation, budget RecoveryBudget) recoveryAllowance {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return recoveryAllowance{CanRecover: false, Reason: "no active failure debt"}
	}
	if signature := repeatedFailureSignature(observations, failureDebt); signature != "" {
		return recoveryAllowance{CanRecover: false, Reason: "structural failure keeps recurring, not retrying: " + signature}
	}
	if recoveryBudgetAllowsStep(observations, budget, recoveryStepCorrectedRetry) {
		return recoveryAllowance{CanRecover: true, Reason: "corrected retry budget remains for " + failureDebt.LatestFailure.Tool}
	}
	if recoveryBudgetAllowsStep(observations, budget, recoveryStepAlternateRoute) {
		return recoveryAllowance{CanRecover: true, Reason: "alternate route budget remains"}
	}
	if recoveryBudgetAllowsStep(observations, budget, recoveryStepAdjacentTool) {
		return recoveryAllowance{CanRecover: true, Reason: "adjacent tool budget remains"}
	}
	return recoveryAllowance{CanRecover: false, Reason: "tool recovery budget exhausted"}
}

// repeatedFailureSignature returns a human-readable signature when the latest
// failure's (tool + stage + error code) has already failed
// structuralFailureRepeatThreshold times. It ignores the tool input, so changing
// cosmetic fields to re-issue the same broken call does not disguise it as fresh
// progress and does not keep the recovery loop banging on a route that keeps
// failing the same way.
func repeatedFailureSignature(observations []turnObservation, failureDebt FailureDebt) string {
	signature := failureSignatureKey(failureDebt.LatestFailure)
	if signature == "" {
		return ""
	}
	occurrences := 0
	for _, observation := range observations {
		if observation.Failed() && failureSignatureKey(observation) == signature {
			occurrences++
		}
	}
	if occurrences < structuralFailureRepeatThreshold {
		return ""
	}
	return failureSignatureLabel(failureDebt.LatestFailure)
}

func failureSignatureKey(observation turnObservation) string {
	tool := strings.TrimSpace(observation.Tool)
	if tool == "" || observation.Failure == nil {
		return ""
	}
	return tool + "\x00" + strings.TrimSpace(observation.Failure.Stage) + "\x00" + strings.TrimSpace(observation.Failure.Code)
}

func failureSignatureLabel(observation turnObservation) string {
	label := strings.TrimSpace(observation.Tool)
	if observation.Failure == nil {
		return label
	}
	if stage := strings.TrimSpace(observation.Failure.Stage); stage != "" {
		label += " at " + stage
	}
	if code := strings.TrimSpace(observation.Failure.Code); code != "" {
		label += " (" + code + ")"
	}
	return label
}

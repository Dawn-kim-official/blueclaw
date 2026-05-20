package agent

type actionProgressEvaluation struct {
	HasProgress                      bool
	Reason                           string
	ProgressEventCount               int
	ConsecutiveNoProgressActionCount int
}

type actionProgressTracker struct {
	lastProgressEventCount           int
	consecutiveNoProgressActionCount int
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

func evaluateRecoveryAllowance(observations []turnObservation, budget RecoveryBudget) recoveryAllowance {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return recoveryAllowance{CanRecover: false, Reason: "no active failure debt"}
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

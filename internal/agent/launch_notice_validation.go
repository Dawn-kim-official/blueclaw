package agent

import (
	"regexp"
	"strings"
)

const launchNoticeReaskEventName = "agent.launch_notice_reask"

const launchNoticeReaskInstruction = "The previous launchNotice violated the no-time-mention rule: launchNotice must never state a specific time estimate, minute count, duration, or internal budget. Rephrase launchNotice as one short acknowledgement in the response language that work has started, with no digits, no duration words, and no numbers of any kind."

var launchNoticeDurationPattern = regexp.MustCompile(`(?i)\d+\s*(분|시간)|(한|두|세|네|반)\s?시간|\d+\s*(minutes?|mins?|hours?|hrs?)\b`)

type launchNoticeReaskReport struct {
	WasAttempted         bool   `json:"wasAttempted"`
	DidRewrite           bool   `json:"didRewrite"`
	OriginalLaunchNotice string `json:"originalLaunchNotice,omitempty"`
	RevisedLaunchNotice  string `json:"revisedLaunchNotice,omitempty"`
	Reason               string `json:"reason,omitempty"`
}

func launchNoticeStatesTimeEstimate(launchNotice string) bool {
	return launchNoticeDurationPattern.MatchString(strings.TrimSpace(launchNotice))
}

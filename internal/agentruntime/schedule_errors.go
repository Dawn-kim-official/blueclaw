package agentruntime

import "errors"

var (
	errScheduleRequesterRequired    = errors.New("requester is required for schedule.create")
	errScheduleConversationRequired = errors.New("conversation target is required for schedule.create")
	errScheduleReplyTargetRequired  = errors.New("reply target is required for schedule.create")
	errSchedulePromptRequired       = errors.New("prompt is required for schedule.create")
	errScheduleTimeZoneInvalid      = errors.New("timeZone must be a valid IANA time zone")
	errScheduleRunAtInvalid         = errors.New("runAt must be RFC3339")
	errScheduleInvalidExpiresAt     = errors.New("expiresAt must be a future RFC3339 timestamp")
	errScheduleFiniteBoundRequired  = errors.New("high-frequency or external repeated schedules require expiresAt or maxRunCount")
)

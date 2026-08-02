package agentcontract

import (
	"strings"
	"time"
)

type CompanyContext struct {
	Name           string
	BrandName      string
	Slogan         string
	Description    string
	Representative string
	Website        string
}

func (company CompanyContext) IsEmpty() bool {
	return company.Name == "" && company.BrandName == "" && company.Description == ""
}

type ScheduledRunContext struct {
	ScheduleID        string `json:"scheduleID,omitempty"`
	Name              string `json:"name,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Cadence           string `json:"cadence,omitempty"`
	CronExpression    string `json:"cronExpression,omitempty"`
	TimeZone          string `json:"timeZone,omitempty"`
	OccurrenceAt      string `json:"occurrenceAt,omitempty"`
	RunAt             string `json:"runAt,omitempty"`
	IntervalSecond    int    `json:"intervalSecond,omitempty"`
	CompletedRunCount int    `json:"completedRunCount,omitempty"`
	MaxRunCount       int    `json:"maxRunCount,omitempty"`
	LastRunAt         string `json:"lastRunAt,omitempty"`
	NextRunAt         string `json:"nextRunAt,omitempty"`
	ExpiresAt         string `json:"expiresAt,omitempty"`
}

func (context ScheduledRunContext) IsEmpty() bool {
	return strings.TrimSpace(context.ScheduleID) == "" &&
		strings.TrimSpace(context.Kind) == "" &&
		strings.TrimSpace(context.OccurrenceAt) == ""
}

type AmbientDutyContext struct {
	IsMatch    bool    `json:"isMatch"`
	Name       string  `json:"name,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

func (context AmbientDutyContext) Normalized() AmbientDutyContext {
	name := strings.TrimSpace(context.Name)
	if !context.IsMatch || name == "" {
		return AmbientDutyContext{}
	}
	confidence := context.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return AmbientDutyContext{
		IsMatch:    true,
		Name:       name,
		Confidence: confidence,
	}
}

type ArtifactManifestEntry struct {
	FileHint       string    `json:"fileHint"`
	TaskRunID      string    `json:"taskRunID"`
	RelativePath   string    `json:"relativePath"`
	ProducingTool  string    `json:"producingTool,omitempty"`
	ProducingSkill string    `json:"producingSkill,omitempty"`
	ModifiedAt     time.Time `json:"modifiedAt"`
}

type DroppedToolGroup struct {
	Name      string   `json:"name"`
	ToolIDs   []string `json:"toolIDs"`
	IsPartial bool     `json:"isPartial"`
}

type ToolExposureEvent struct {
	ValidSelectedToolIDs []string           `json:"validSelectedToolIDs,omitempty"`
	SelectionReason      string             `json:"selectionReason,omitempty"`
	SelectionSource      string             `json:"selectionSource,omitempty"`
	UsedFallbackGroups   bool               `json:"usedFallbackGroups"`
	ExposedToolIDs       []string           `json:"exposedToolIDs"`
	SelectedSkillToolIDs []string           `json:"selectedSkillToolIDs,omitempty"`
	PinnedGroupToolIDs   []string           `json:"pinnedGroupToolIDs,omitempty"`
	DroppedGroups        []DroppedToolGroup `json:"droppedGroups,omitempty"`
}

type FailureNotice struct {
	Message           string `json:"message,omitempty"`
	Source            string `json:"source,omitempty"`
	Language          string `json:"language,omitempty"`
	DiagnosticEventID string `json:"diagnosticEventID,omitempty"`
	IsSendable        bool   `json:"isSendable,omitempty"`
}

func (notice FailureNotice) SendableMessage() string {
	if !notice.IsSendable {
		return ""
	}
	return strings.TrimSpace(notice.Message)
}

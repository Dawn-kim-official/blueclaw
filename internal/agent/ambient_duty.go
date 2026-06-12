package agent

import "strings"

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

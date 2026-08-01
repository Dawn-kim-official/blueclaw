package model

import (
	"errors"
	"strings"
)

type structuredOutputCorrectionError interface {
	error
	StructuredOutputCorrection() (StructuredOutputCorrection, bool)
}

type structuredOutputDiagnosticError interface {
	error
	StructuredOutputDiagnostic() (StructuredOutputDiagnostic, bool)
}

type StructuredOutputDiagnosticCategory string

type StructuredOutputFinishReason string

type StructuredOutputValidationCode string

type StructuredOutputRepairStatus string

const (
	StructuredOutputDiagnosticJSONParse        StructuredOutputDiagnosticCategory = "json_parse"
	StructuredOutputDiagnosticSchemaValidation StructuredOutputDiagnosticCategory = "schema_validation"
	StructuredOutputDiagnosticFinishReason     StructuredOutputDiagnosticCategory = "finish_reason"
	StructuredOutputDiagnosticEmptyCompletion  StructuredOutputDiagnosticCategory = "empty_completion"
	StructuredOutputDiagnosticToolCallContract StructuredOutputDiagnosticCategory = "tool_call_contract"
	StructuredOutputDiagnosticSerialization    StructuredOutputDiagnosticCategory = "serialization"
)

const (
	StructuredOutputDiagnosticFinishStop          StructuredOutputFinishReason = "stop"
	StructuredOutputDiagnosticFinishLength        StructuredOutputFinishReason = "length"
	StructuredOutputDiagnosticFinishToolCalls     StructuredOutputFinishReason = "tool_calls"
	StructuredOutputDiagnosticFinishContentFilter StructuredOutputFinishReason = "content_filter"
	StructuredOutputDiagnosticFinishError         StructuredOutputFinishReason = "error"
	StructuredOutputDiagnosticFinishOther         StructuredOutputFinishReason = "other"
	StructuredOutputDiagnosticFinishUnknown       StructuredOutputFinishReason = "unknown"
)

const (
	StructuredOutputValidationRequired           StructuredOutputValidationCode = "required"
	StructuredOutputValidationAdditionalProperty StructuredOutputValidationCode = "additional_property"
	StructuredOutputValidationType               StructuredOutputValidationCode = "type"
	StructuredOutputValidationOther              StructuredOutputValidationCode = "other"
)

const (
	StructuredOutputRepairNotAttempted StructuredOutputRepairStatus = "not_attempted"
	StructuredOutputRepairFailed       StructuredOutputRepairStatus = "failed"
)

type StructuredOutputValidationIssue struct {
	FieldPath string                         `json:"fieldPath"`
	Code      StructuredOutputValidationCode `json:"code"`
}

type StructuredOutputDiagnostic struct {
	Category         StructuredOutputDiagnosticCategory
	FinishReason     StructuredOutputFinishReason
	ToolName         string
	ValidationIssues []StructuredOutputValidationIssue
	RepairStatus     StructuredOutputRepairStatus
}

type StructuredOutputCorrection struct {
	Code       string
	Diagnostic StructuredOutputDiagnostic
}

func StructuredOutputCorrectionFromError(errorValue error) (StructuredOutputCorrection, bool) {
	var correctionError structuredOutputCorrectionError
	if !errors.As(errorValue, &correctionError) {
		return StructuredOutputCorrection{}, false
	}
	correction, isCorrectable := correctionError.StructuredOutputCorrection()
	if !isCorrectable || !isCorrectableStructuredOutputCode(correction.Code) || !isCorrectableStructuredOutputCategory(correction.Diagnostic.Category) {
		return StructuredOutputCorrection{}, false
	}
	return correction, true
}

func isCorrectableStructuredOutputCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "provider_response_invalid", "structured_output_invalid":
		return true
	default:
		return false
	}
}

func isCorrectableStructuredOutputCategory(category StructuredOutputDiagnosticCategory) bool {
	switch category {
	case StructuredOutputDiagnosticJSONParse,
		StructuredOutputDiagnosticSchemaValidation,
		StructuredOutputDiagnosticFinishReason,
		StructuredOutputDiagnosticToolCallContract:
		return true
	default:
		return false
	}
}

func StructuredOutputDiagnosticFromError(errorValue error) (StructuredOutputDiagnostic, bool) {
	var diagnosticError structuredOutputDiagnosticError
	if !errors.As(errorValue, &diagnosticError) {
		return StructuredOutputDiagnostic{}, false
	}
	diagnostic, hasDiagnostic := diagnosticError.StructuredOutputDiagnostic()
	if !hasDiagnostic || diagnostic.Category == "" {
		return StructuredOutputDiagnostic{}, false
	}
	return diagnostic, true
}

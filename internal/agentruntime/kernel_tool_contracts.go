package agentruntime

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	terminalRunModeCommand       = "command"
	terminalRunModeSessionStart  = "session_start"
	terminalRunModeSessionWrite  = "session_write"
	terminalRunModeSessionStatus = "session_status"
	terminalRunModeSessionClose  = "session_close"
)

var terminalRunInputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"mode":{"type":"string","enum":["command","session_start","session_write","session_status","session_close"]},
		"approvalRequired":{"type":"boolean"},
		"approvalReason":{"type":"string","minLength":1},
		"command":{"type":"string","minLength":1},
		"executableName":{"type":"string","minLength":1},
		"arguments":{"type":"array","items":{"type":"string"}},
		"stdin":{"type":"string"},
		"workingDirectoryPath":{"type":"string"},
		"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},
		"timeoutSecond":{"type":"integer","minimum":1},
		"sessionID":{"type":"string","minLength":1},
		"input":{"type":"string","minLength":1}
	},
	"allOf":[{
		"oneOf":[
			{
				"properties":{
					"approvalRequired":{"const":true},
					"approvalReason":{"type":"string","minLength":1}
				},
				"required":["approvalRequired","approvalReason"]
			},
			{
				"properties":{"approvalRequired":{"const":false}},
				"not":{"required":["approvalReason"]}
			}
		]
	}],
	"additionalProperties":false
}`)

var terminalRunInputIntentSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"mode":{"type":"string","enum":["command","session_start","session_write","session_status","session_close"]},
		"command":{"type":"string","minLength":1},
		"executableName":{"type":"string","minLength":1},
		"arguments":{"type":"array","items":{"type":"string"}},
		"stdin":{"type":"string"},
		"workingDirectoryPath":{"type":"string"},
		"environmentVariables":{"type":"object","additionalProperties":{"type":"string"}},
		"timeoutSecond":{"type":"integer","minimum":1},
		"sessionID":{"type":"string","minLength":1},
		"input":{"type":"string","minLength":1}
	},
	"additionalProperties":false
}`)

var terminalRunResultSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"mode":{"type":"string"},
		"completed":{"type":"boolean"},
		"exitCode":{"type":"integer"},
		"stdout":{"type":"string"},
		"stderr":{"type":"string"},
		"timedOut":{"type":"boolean"},
		"outputTrimmed":{"type":"boolean"},
		"sessionID":{"type":"string"},
		"status":{"type":"string"},
		"recentOutput":{"type":"string"}
	},
	"required":["mode","completed"],
	"additionalProperties":false,
	"oneOf":[
		{
			"type":"object",
			"properties":{
				"mode":{"const":"command"},
				"completed":{"type":"boolean"},
				"exitCode":{"type":"integer"},
				"stdout":{"type":"string"},
				"stderr":{"type":"string"},
				"timedOut":{"type":"boolean"},
				"outputTrimmed":{"type":"boolean"}
			},
			"required":["mode","completed","exitCode","stdout","stderr","timedOut","outputTrimmed"],
			"additionalProperties":false,
			"oneOf":[
				{
					"properties":{"completed":{"const":true},"exitCode":{"const":0},"timedOut":{"const":false}},
					"required":["completed","exitCode","timedOut"]
				},
				{
					"properties":{"completed":{"const":false}},
					"required":["completed"],
					"not":{
						"properties":{"exitCode":{"const":0},"timedOut":{"const":false}},
						"required":["exitCode","timedOut"]
					}
				}
			]
		},
		{
			"type":"object",
			"properties":{
				"mode":{"type":"string","enum":["session_start","session_write"]},
				"completed":{"const":false},
				"sessionID":{"type":"string","minLength":1},
				"status":{"type":"string","enum":["running","exited"]},
				"exitCode":{"type":"integer"},
				"stdout":{"type":"string"},
				"stderr":{"type":"string"},
				"recentOutput":{"type":"string"},
				"outputTrimmed":{"type":"boolean"}
			},
			"required":["mode","completed","sessionID","status","exitCode","stdout","stderr","recentOutput","outputTrimmed"],
			"additionalProperties":false
		},
		{
			"type":"object",
			"properties":{
				"mode":{"const":"session_status"},
				"completed":{"type":"boolean"},
				"sessionID":{"type":"string","minLength":1},
				"status":{"type":"string","enum":["running","exited"]},
				"exitCode":{"type":"integer"},
				"stdout":{"type":"string"},
				"stderr":{"type":"string"},
				"recentOutput":{"type":"string"},
				"outputTrimmed":{"type":"boolean"}
			},
			"required":["mode","completed","sessionID","status","exitCode","stdout","stderr","recentOutput","outputTrimmed"],
			"additionalProperties":false,
			"oneOf":[
				{
					"properties":{"completed":{"const":true},"status":{"const":"exited"},"exitCode":{"const":0}},
					"required":["completed","status","exitCode"]
				},
				{
					"properties":{"completed":{"const":false}},
					"required":["completed"],
					"not":{
						"properties":{"status":{"const":"exited"},"exitCode":{"const":0}},
						"required":["status","exitCode"]
					}
				}
			]
		},
		{
			"type":"object",
			"properties":{
				"mode":{"const":"session_close"},
				"completed":{"const":false},
				"sessionID":{"type":"string","minLength":1},
				"status":{"const":"closed"}
			},
			"required":["mode","completed","sessionID","status"],
			"additionalProperties":false
		}
	]
}`)

type terminalRunToolInput struct {
	Mode                 string            `json:"mode"`
	ApprovalRequired     *bool             `json:"approvalRequired"`
	ApprovalReason       string            `json:"approvalReason"`
	Command              string            `json:"command"`
	ExecutableName       string            `json:"executableName"`
	Arguments            []string          `json:"arguments"`
	Stdin                string            `json:"stdin"`
	WorkingDirectoryPath string            `json:"workingDirectoryPath"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
	TimeoutSecond        int               `json:"timeoutSecond"`
	SessionID            string            `json:"sessionID"`
	Input                string            `json:"input"`
}

type terminalCommandResultDocument struct {
	Mode          string `json:"mode"`
	Completed     bool   `json:"completed"`
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	TimedOut      bool   `json:"timedOut"`
	OutputTrimmed bool   `json:"outputTrimmed"`
}

type terminalSessionResultDocument struct {
	Mode          string `json:"mode"`
	Completed     bool   `json:"completed"`
	SessionID     string `json:"sessionID"`
	Status        string `json:"status"`
	ExitCode      int    `json:"exitCode"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	RecentOutput  string `json:"recentOutput"`
	OutputTrimmed bool   `json:"outputTrimmed"`
}

type terminalSessionCloseResultDocument struct {
	Mode      string `json:"mode"`
	Completed bool   `json:"completed"`
	SessionID string `json:"sessionID"`
	Status    string `json:"status"`
}

func normalizedTerminalRunMode(mode string) string {
	trimmedMode := strings.TrimSpace(mode)
	if trimmedMode == "" {
		return terminalRunModeCommand
	}
	return trimmedMode
}

func validateTerminalRunInput(input terminalRunToolInput) error {
	isApprovalRequired := input.ApprovalRequired != nil && *input.ApprovalRequired
	if isApprovalRequired != (strings.TrimSpace(input.ApprovalReason) != "") {
		return errors.New("approvalReason is required exactly when approvalRequired is true")
	}
	switch normalizedTerminalRunMode(input.Mode) {
	case terminalRunModeCommand:
		return validateTerminalCommandInput(input)
	case terminalRunModeSessionStart:
		return validateTerminalSessionStartInput(input)
	case terminalRunModeSessionWrite:
		return validateTerminalSessionWriteInput(input)
	case terminalRunModeSessionStatus, terminalRunModeSessionClose:
		return validateTerminalSessionLookupInput(input)
	default:
		return errors.New("terminal.run mode is invalid")
	}
}

func validateTerminalCommandInput(input terminalRunToolInput) error {
	hasCommand := strings.TrimSpace(input.Command) != ""
	hasExecutable := strings.TrimSpace(input.ExecutableName) != ""
	if hasCommand == hasExecutable {
		return errors.New("command mode requires exactly one of command or executableName")
	}
	if hasCommand && len(input.Arguments) > 0 {
		return errors.New("arguments are only valid with executableName")
	}
	if strings.TrimSpace(input.SessionID) != "" || input.Input != "" {
		return errors.New("command mode does not accept sessionID or input")
	}
	return nil
}

func validateTerminalSessionStartInput(input terminalRunToolInput) error {
	if strings.TrimSpace(input.Command) == "" {
		return errors.New("session_start requires command")
	}
	if strings.TrimSpace(input.ExecutableName) != "" || len(input.Arguments) > 0 || input.Stdin != "" || strings.TrimSpace(input.SessionID) != "" || input.Input != "" {
		return errors.New("session_start accepts command configuration only")
	}
	return nil
}

func validateTerminalSessionWriteInput(input terminalRunToolInput) error {
	if strings.TrimSpace(input.SessionID) == "" || input.Input == "" {
		return errors.New("session_write requires sessionID and input")
	}
	if hasTerminalCommandConfiguration(input) || hasTerminalRuntimeConfiguration(input) || input.ApprovalRequired != nil {
		return errors.New("session_write accepts sessionID and input only")
	}
	return nil
}

func validateTerminalSessionLookupInput(input terminalRunToolInput) error {
	if strings.TrimSpace(input.SessionID) == "" {
		return errors.New("sessionID is required")
	}
	if hasTerminalCommandConfiguration(input) || hasTerminalRuntimeConfiguration(input) || input.Input != "" || input.ApprovalRequired != nil {
		return errors.New("session_status and session_close accept sessionID only")
	}
	return nil
}

func hasTerminalCommandConfiguration(input terminalRunToolInput) bool {
	return strings.TrimSpace(input.Command) != "" ||
		strings.TrimSpace(input.ExecutableName) != "" ||
		len(input.Arguments) > 0 ||
		input.Stdin != ""
}

func hasTerminalRuntimeConfiguration(input terminalRunToolInput) bool {
	return strings.TrimSpace(input.WorkingDirectoryPath) != "" ||
		len(input.EnvironmentVariables) > 0 ||
		input.TimeoutSecond != 0
}

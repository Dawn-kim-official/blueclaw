package enrollment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/internal/config"
	"github.com/Dawn-kim-official/blueclaw/internal/security"
)

func Materialize(home Home, enrollment Enrollment) error {
	if errorValue := enrollment.Validate(); errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(home.DirectoryPath, 0o700); errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(enrollment.WorkspaceRootPath, 0o755); errorValue != nil {
		return errorValue
	}
	if errorValue := writeMigrations(filepath.Join(home.DirectoryPath, "migrations")); errorValue != nil {
		return errorValue
	}
	if errorValue := writeJSONDocument(home.RuntimeConfigurationPath(), runtimeConfigurationFor(home, enrollment)); errorValue != nil {
		return errorValue
	}
	return writeJSONDocument(home.PolicyPath(), policyDocumentFor(enrollment))
}

func runtimeConfigurationFor(home Home, enrollment Enrollment) config.RuntimeConfiguration {
	runtimeConfiguration := config.RuntimeConfiguration{
		BaseURL: "http://" + availableListenAddress(),
		Database: config.DatabaseConfiguration{
			Driver:                 "postgres",
			ConnectionString:       enrollment.DatabaseConnectionString,
			MigrationDirectoryPath: filepath.Join(home.DirectoryPath, "migrations"),
		},
		Terminal: config.TerminalConfiguration{
			Mode:              terminalModeForPlatform(),
			WorkspaceRootPath: enrollment.WorkspaceRootPath,
			TimeoutSecond:     600,
		},
		Agent: config.AgentConfiguration{
			Intake: config.AgentIntakeConfiguration{Enabled: true, ExecutionMode: "auto"},
			Harness: config.HarnessConfiguration{
				Name:             enrollment.Harness.Name,
				AgentCommandPath: enrollment.Harness.AgentCommandPath,
			},
		},
	}
	runtimeConfiguration.Connectors.EnrolSenders = enrollment.Messenger.IsConnected() && enrollment.Messenger.IsOpenToPeople
	switch enrollment.Messenger.Name {
	case MessengerMattermost:
		runtimeConfiguration.Connectors.Mattermost.BaseURL = strings.TrimSpace(enrollment.Messenger.BaseURL)
	case MessengerBuzz:
		runtimeConfiguration.Connectors.Buzz.Enabled = true
	}
	if socketPath := strings.TrimSpace(enrollment.LanguageModel.LLMDUnixSocketPath); socketPath != "" {
		runtimeConfiguration.LanguageModel.DefaultProvider = "llmd"
		runtimeConfiguration.LanguageModel.LLMD.UnixSocketPath = socketPath
		runtimeConfiguration.LanguageModel.LLMD.AuthKeyPath = strings.TrimSpace(enrollment.LanguageModel.LLMDAuthKeyPath)
		runtimeConfiguration.LanguageModel.LLMD.ExecutionMode = "auto"
	}
	return runtimeConfiguration
}

func terminalModeForPlatform() string {
	if runtime.GOOS == "linux" {
		return "native"
	}
	return security.TerminalModeSingleUser
}

func policyDocumentFor(enrollment Enrollment) map[string]any {
	return map[string]any{
		"people": []map[string]any{{
			"personID":          enrollment.Operator.PersonID,
			"displayName":       enrollment.Operator.DisplayName,
			"emails":            []string{enrollment.Operator.Email},
			"securityLevelName": "admin",
			"securityLevelRank": 100,
			"grantedClasses":    []string{"internal"},
			"circles":           []string{"staff"},
			"isAdmin":           true,
		}},
		"circles": []map[string]any{{
			"circleID":               "staff",
			"displayName":            "Staff",
			"workspaceDirectoryPath": filepath.Join(enrollment.WorkspaceRootPath, "circles", "staff"),
		}},
	}
}

func writeJSONDocument(path string, document any) error {
	encodedDocument, errorValue := json.MarshalIndent(document, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(path, append(encodedDocument, '\n'), 0o600)
}

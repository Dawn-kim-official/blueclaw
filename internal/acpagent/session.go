package acpagent

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
	"github.com/Dawn-kim-official/blueclaw/taskstate"
)

type session struct {
	sessionID         acp.SessionId
	workspaceRootPath string
	taskRunID         string
	toolCallCount     int
	mutex             sync.Mutex
}

func (promptSession *session) nextToolCallIdentity() acp.ToolCallId {
	promptSession.toolCallCount++
	return acp.ToolCallId(string(promptSession.sessionID) + "-tool-" + strconv.Itoa(promptSession.toolCallCount))
}

func continuationTaskRunID(turnResult agentcontract.AgentTurnResult) string {
	if isTerminalTaskStatus(turnResult.TaskRun.Status) {
		return ""
	}
	return turnResult.TaskRun.TaskRunID
}

func isTerminalTaskStatus(taskStatus taskstate.TaskStatus) bool {
	switch taskStatus {
	case taskstate.TaskStatusCompleted, taskstate.TaskStatusFailed, taskstate.TaskStatusCancelled:
		return true
	}
	return false
}

func newRandomSessionIdentity() string {
	identityBytes := make([]byte, 16)
	if _, errorValue := rand.Read(identityBytes); errorValue != nil {
		return "session"
	}
	return "session-" + hex.EncodeToString(identityBytes)
}

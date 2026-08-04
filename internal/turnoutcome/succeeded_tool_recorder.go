package turnoutcome

import "sync"

type SucceededToolRecorder struct {
	mutex     sync.Mutex
	toolNames []string
}

func (recorder *SucceededToolRecorder) Observe(toolName string, isSucceeded bool) {
	if !isSucceeded {
		return
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	for _, recordedToolName := range recorder.toolNames {
		if recordedToolName == toolName {
			return
		}
	}
	recorder.toolNames = append(recorder.toolNames, toolName)
}

func (recorder *SucceededToolRecorder) SucceededToolNames() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]string{}, recorder.toolNames...)
}

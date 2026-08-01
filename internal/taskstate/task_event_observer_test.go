package taskstate

import (
	"sync"
	"testing"
)

func TestTaskRunObserverConcurrentAppendUnregisterNoPanic(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		taskEventService := NewTaskEventService()
		events := make(chan struct{}, 16)
		var appendGroup sync.WaitGroup
		appendGroup.Add(1)
		go func() {
			defer appendGroup.Done()
			for appendIndex := 0; appendIndex < 200; appendIndex++ {
				taskEventService.AppendTaskEvent("run-1", "tool.x.result", "{}")
			}
		}()
		unregister := taskEventService.RegisterTaskRunObserver("run-1", func(RawTurnEvent) {
			select {
			case events <- struct{}{}:
			default:
			}
		})
		unregister()
		close(events)
		appendGroup.Wait()
	}
}

func TestRegisterTurnObserverGlobalReceivesUntilUnregister(t *testing.T) {
	taskEventService := NewTaskEventService()
	received := []string{}
	unregister := taskEventService.RegisterTurnObserver(func(rawTurnEvent RawTurnEvent) {
		received = append(received, rawTurnEvent.Name)
	})
	taskEventService.AppendTaskEvent("run-1", "tool.x.requested", "{}")
	taskEventService.AppendTaskEvent("run-2", "tool.y.requested", "{}")
	unregister()
	taskEventService.AppendTaskEvent("run-3", "tool.z.requested", "{}")
	if len(received) != 2 || received[0] != "tool.x.requested" || received[1] != "tool.y.requested" {
		t.Fatalf("expected events from any task run before unregister only, got %v", received)
	}
}

func TestTaskRunObserverWithoutRegistrationPersistsIdentically(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskEventService.AppendTaskEvent("run-1", "tool.x.result", "body")
	stored := taskEventService.ListTaskEvent("run-1")
	if len(stored) != 1 {
		t.Fatalf("expected one persisted event, got %d", len(stored))
	}
	if stored[0].Name != "tool.x.result" || stored[0].Body != "body" {
		t.Fatalf("expected persisted event unchanged, got %+v", stored[0])
	}
}

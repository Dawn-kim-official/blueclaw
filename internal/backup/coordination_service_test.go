package backup

import (
	"testing"
	"time"
)

func TestCoordinatorPausesAndResumesIngress(t *testing.T) {
	coordinator := NewCoordinator(Manifest{ContractVersion: 1})

	status := coordinator.Prepare("internkim-admind")
	if !status.IsPaused {
		t.Fatal("expected prepare to pause ingress")
	}
	if !coordinator.IsPaused() {
		t.Fatal("expected coordinator to report paused")
	}

	status = coordinator.Complete()
	if status.IsPaused {
		t.Fatal("expected complete to resume ingress")
	}
	if coordinator.IsPaused() {
		t.Fatal("expected coordinator to report resumed")
	}
}

func TestCoordinatorExpiresStalePrepareLock(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	coordinator := NewCoordinator(Manifest{ContractVersion: 1})
	coordinator.now = func() time.Time { return now }
	coordinator.Prepare("internkim-admind")

	now = now.Add(6 * time.Minute)
	if coordinator.IsPaused() {
		t.Fatal("expected stale prepare lock to expire")
	}
}

package backup

import (
	"sync"
	"time"
)

type Manifest struct {
	ContractVersion         int      `json:"contractVersion"`
	BlueclawVersion         string   `json:"blueclawVersion"`
	SchemaVersion           string   `json:"schemaVersion"`
	PersistentDataRoots     []string `json:"persistentDataRoots"`
	DatabaseKind            string   `json:"databaseKind"`
	RequiredBackupArtifacts []string `json:"requiredBackupArtifacts"`
}

type LockStatus struct {
	IsPaused  bool      `json:"isPaused"`
	Holder    string    `json:"holder,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

type Coordinator struct {
	mutex    sync.Mutex
	manifest Manifest
	lock     LockStatus
	now      func() time.Time
	ttl      time.Duration
}

func NewCoordinator(manifest Manifest) *Coordinator {
	return &Coordinator{
		manifest: manifest,
		now:      time.Now,
		ttl:      5 * time.Minute,
	}
}

func (coordinator *Coordinator) Manifest() Manifest {
	return coordinator.manifest
}

func (coordinator *Coordinator) Prepare(holder string) LockStatus {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	now := coordinator.now().UTC()
	coordinator.lock = LockStatus{
		IsPaused:  true,
		Holder:    holder,
		ExpiresAt: now.Add(coordinator.ttl),
	}
	return coordinator.lock
}

func (coordinator *Coordinator) Complete() LockStatus {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	coordinator.lock = LockStatus{}
	return coordinator.lock
}

func (coordinator *Coordinator) Status() LockStatus {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	coordinator.expireIfNeeded()
	return coordinator.lock
}

func (coordinator *Coordinator) IsPaused() bool {
	return coordinator.Status().IsPaused
}

func (coordinator *Coordinator) expireIfNeeded() {
	if !coordinator.lock.IsPaused {
		return
	}
	if coordinator.now().UTC().After(coordinator.lock.ExpiresAt) {
		coordinator.lock = LockStatus{}
	}
}

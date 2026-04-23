package backup

import "time"

type SnapshotBundle struct {
	BundlePath    string    `json:"bundlePath"`
	IncludedPaths []string  `json:"includedPaths"`
	CreatedAt     time.Time `json:"createdAt"`
}

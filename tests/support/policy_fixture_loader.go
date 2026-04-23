package support

import (
	"os"
	"path/filepath"
)

func PolicyFixturePath() string {
	return filepath.Join("..", "..", "config", "policy.example.yaml")
}

func ReadPolicyFixture() ([]byte, error) {
	return os.ReadFile(PolicyFixturePath())
}

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DiskSpaceWarningThreshold = 100 * 1024 * 1024

type Memory struct {
	Subject        string    `yaml:"subject"`
	RecallCount    int       `yaml:"recallCount"`
	CreatedAt      time.Time `yaml:"createdAt"`
	LastRecalledAt time.Time `yaml:"lastRecalledAt,omitempty"`
	Storage        string    `yaml:"storage"`
	Content        string    `yaml:"-"`
}

type Store struct {
	blueclawDirectory string
}

func NewStore(blueclawDirectory string) *Store {
	return &Store{blueclawDirectory: blueclawDirectory}
}

func (store *Store) Save(subject string, content string) (string, error) {
	slug := Slugify(subject)
	storageDirectory := filepath.Join(store.blueclawDirectory, "short-term-memory")
	if err := os.MkdirAll(storageDirectory, 0755); err != nil {
		return "", fmt.Errorf("creating memory directory: %w", err)
	}
	filePath := filepath.Join(storageDirectory, slug+".md")
	memory := Memory{
		Subject:   subject,
		CreatedAt: time.Now(),
		Storage:   "short-term",
		Content:   content,
	}
	if existing, err := store.readMemoryFile(filePath); err == nil {
		memory.CreatedAt = existing.CreatedAt
		memory.RecallCount = existing.RecallCount
		memory.LastRecalledAt = existing.LastRecalledAt
		memory.Storage = existing.Storage
	}
	if err := store.writeMemoryFile(filePath, memory); err != nil {
		return "", err
	}
	relativePath := filepath.Join(memory.Storage+"-memory", slug+".md")
	return relativePath, nil
}

func (store *Store) Load(storage string, slug string) (Memory, error) {
	storageDirectory := filepath.Join(store.blueclawDirectory, storage+"-memory")
	filePath := filepath.Join(storageDirectory, slug+".md")
	return store.readMemoryFile(filePath)
}

func (store *Store) IncrementRecallCount(memory *Memory) error {
	memory.RecallCount++
	memory.LastRecalledAt = time.Now()
	slug := Slugify(memory.Subject)
	storageDirectory := filepath.Join(store.blueclawDirectory, memory.Storage+"-memory")
	filePath := filepath.Join(storageDirectory, slug+".md")
	return store.writeMemoryFile(filePath, *memory)
}

func (store *Store) ListMemories(storage string) ([]Memory, error) {
	storageDirectory := filepath.Join(store.blueclawDirectory, storage+"-memory")
	entries, err := os.ReadDir(storageDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading memory directory: %w", err)
	}
	memories := make([]Memory, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		filePath := filepath.Join(storageDirectory, entry.Name())
		memory, err := store.readMemoryFile(filePath)
		if err != nil {
			continue
		}
		memories = append(memories, memory)
	}
	return memories, nil
}

func (store *Store) writeMemoryFile(filePath string, memory Memory) error {
	frontmatter, err := yaml.Marshal(memory)
	if err != nil {
		return fmt.Errorf("marshaling memory frontmatter: %w", err)
	}
	fileContent := fmt.Sprintf("---\n%s---\n\n%s\n", string(frontmatter), memory.Content)
	return os.WriteFile(filePath, []byte(fileContent), 0644)
}

func (store *Store) readMemoryFile(filePath string) (Memory, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Memory{}, fmt.Errorf("reading memory file: %w", err)
	}
	return parseMemoryFile(string(data))
}

func parseMemoryFile(content string) (Memory, error) {
	if !strings.HasPrefix(content, "---\n") {
		return Memory{}, fmt.Errorf("memory file missing frontmatter")
	}
	endIndex := strings.Index(content[4:], "---\n")
	if endIndex == -1 {
		return Memory{}, fmt.Errorf("memory file missing frontmatter end marker")
	}
	frontmatterContent := content[4 : 4+endIndex]
	bodyContent := strings.TrimSpace(content[4+endIndex+4:])
	var memory Memory
	if err := yaml.Unmarshal([]byte(frontmatterContent), &memory); err != nil {
		return Memory{}, fmt.Errorf("parsing memory frontmatter: %w", err)
	}
	memory.Content = bodyContent
	return memory, nil
}

var slugRegexp = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(text string) string {
	lowered := strings.ToLower(text)
	slug := slugRegexp.ReplaceAllString(lowered, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "untitled"
	}
	return slug
}

package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	PromotionThreshold  = 3
	DefaultEpisodeTTL   = 7 * 24 * time.Hour
	ExpirationExtension = 7 * 24 * time.Hour
)

type MemoryType string

const (
	MemoryTypeFact       MemoryType = "fact"
	MemoryTypePreference MemoryType = "preference"
	MemoryTypeEpisode    MemoryType = "episode"
)

type Memory struct {
	ID             int64
	Type           MemoryType
	Title          string
	Content        string
	RecallCount    int
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	LastRecalledAt *time.Time
}

type Connection struct {
	FromID    int64
	ToID      int64
	Relation  string
	CreatedAt time.Time
}

type GraphStore struct {
	database *sql.DB
}

func ensureDatabaseDirectory(databasePath string) error {
	dir := filepath.Dir(databasePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating database directory %s: %w", dir, err)
	}
	return nil
}

func NewGraphStore(databasePath string) (*GraphStore, error) {
	ensureVecRegistered()
	if err := ensureDatabaseDirectory(databasePath); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite3", databasePath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("opening graph store database: %w", err)
	}
	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if _, err := database.Exec("PRAGMA foreign_keys = ON"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	store := &GraphStore{database: database}
	if err := checkDatabaseIntegrity(store); err != nil {
		database.Close()
		return nil, err
	}
	if err := createGraphSchema(database); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func (store *GraphStore) Close() error {
	return store.database.Close()
}

func createGraphSchema(database *sql.DB) error {
	memoriesSchema := `CREATE TABLE IF NOT EXISTS memories (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		type             TEXT NOT NULL CHECK(type IN ('fact', 'preference', 'episode')),
		title            TEXT NOT NULL UNIQUE,
		content          TEXT NOT NULL,
		recall_count     INTEGER NOT NULL DEFAULT 0,
		expires_at       DATETIME NULL,
		created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_recalled_at DATETIME NULL
	)`
	if _, err := database.Exec(memoriesSchema); err != nil {
		return fmt.Errorf("creating memories table: %w", err)
	}
	connectionsSchema := `CREATE TABLE IF NOT EXISTS memory_connections (
		from_id    INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		to_id      INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		relation   TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (from_id, to_id)
	)`
	if _, err := database.Exec(connectionsSchema); err != nil {
		return fmt.Errorf("creating memory_connections table: %w", err)
	}
	vectorSchema := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_memories USING vec0(
		id        INTEGER PRIMARY KEY,
		embedding FLOAT[%d]
	)`, EmbeddingDimension)
	if _, err := database.Exec(vectorSchema); err != nil {
		return fmt.Errorf("creating vec_memories table: %w", err)
	}
	return nil
}

func (store *GraphStore) Save(title, content string, memType MemoryType, expiresAt *time.Time) (int64, error) {
	result, err := store.database.Exec(`
		INSERT INTO memories (type, title, content, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(title) DO UPDATE SET
			content = excluded.content,
			type = excluded.type,
			expires_at = excluded.expires_at
	`, string(memType), title, content, expiresAt)
	if err != nil {
		return 0, fmt.Errorf("saving memory %q: %w", title, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting memory id for %q: %w", title, err)
	}
	if id == 0 {
		if err := store.database.QueryRow("SELECT id FROM memories WHERE title = ?", title).Scan(&id); err != nil {
			return 0, fmt.Errorf("looking up memory id for %q: %w", title, err)
		}
	}
	return id, nil
}

func (store *GraphStore) Load(title string) (Memory, error) {
	var m Memory
	var expiresAt, lastRecalledAt sql.NullString
	err := store.database.QueryRow(`
		SELECT id, type, title, content, recall_count, expires_at, created_at, last_recalled_at
		FROM memories WHERE title = ?
	`, title).Scan(&m.ID, &m.Type, &m.Title, &m.Content, &m.RecallCount, &expiresAt, &m.CreatedAt, &lastRecalledAt)
	if err == sql.ErrNoRows {
		return Memory{}, fmt.Errorf("memory %q not found", title)
	}
	if err != nil {
		return Memory{}, fmt.Errorf("loading memory %q: %w", title, err)
	}
	m.ExpiresAt = parseNullableTime(expiresAt)
	m.LastRecalledAt = parseNullableTime(lastRecalledAt)
	return m, nil
}

func (store *GraphStore) Connect(fromID, toID int64, relation string) error {
	if fromID == toID {
		return fmt.Errorf("cannot connect a memory to itself")
	}
	_, err := store.database.Exec(`
		INSERT INTO memory_connections (from_id, to_id, relation)
		VALUES (?, ?, ?)
		ON CONFLICT(from_id, to_id) DO UPDATE SET relation = excluded.relation
	`, fromID, toID, relation)
	if err != nil {
		return fmt.Errorf("creating connection from %d to %d: %w", fromID, toID, err)
	}
	return nil
}

func (store *GraphStore) Neighbors(id int64) ([]Memory, error) {
	rows, err := store.database.Query(`
		SELECT id, type, title, content, recall_count, expires_at, created_at, last_recalled_at
		FROM memories WHERE id IN (
			SELECT to_id FROM memory_connections WHERE from_id = ?
			UNION
			SELECT from_id FROM memory_connections WHERE to_id = ?
		)
	`, id, id)
	if err != nil {
		return nil, fmt.Errorf("fetching neighbors of %d: %w", id, err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (store *GraphStore) IncrementRecall(id int64) error {
	_, err := store.database.Exec(`
		UPDATE memories SET recall_count = recall_count + 1, last_recalled_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("incrementing recall for memory %d: %w", id, err)
	}
	return nil
}

func (store *GraphStore) ExtendExpiration(id int64, duration time.Duration) error {
	newExpiry := time.Now().Add(duration)
	_, err := store.database.Exec("UPDATE memories SET expires_at = ? WHERE id = ?", newExpiry, id)
	if err != nil {
		return fmt.Errorf("extending expiration for memory %d: %w", id, err)
	}
	return nil
}

func (store *GraphStore) Promote(id int64) error {
	_, err := store.database.Exec("UPDATE memories SET expires_at = NULL WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("promoting memory %d: %w", id, err)
	}
	return nil
}

func (store *GraphStore) CleanupExpired() error {
	_, err := store.database.Exec("DELETE FROM memories WHERE expires_at IS NOT NULL AND datetime(expires_at) < datetime('now')")
	if err != nil {
		return fmt.Errorf("cleaning up expired memories: %w", err)
	}
	return nil
}

func (store *GraphStore) SaveEmbedding(id int64, embedding []float32) error {
	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshaling embedding: %w", err)
	}
	transaction, err := store.database.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction for embedding %d: %w", id, err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("DELETE FROM vec_memories WHERE id = ?", id); err != nil {
		return fmt.Errorf("clearing embedding for memory %d: %w", id, err)
	}
	if _, err := transaction.Exec("INSERT INTO vec_memories (id, embedding) VALUES (?, ?)", id, string(embeddingJSON)); err != nil {
		return fmt.Errorf("inserting embedding for memory %d: %w", id, err)
	}
	return transaction.Commit()
}

func (store *GraphStore) Recent(limit int) ([]Memory, error) {
	rows, err := store.database.Query(`
		SELECT id, type, title, content, recall_count, expires_at, created_at, last_recalled_at
		FROM memories
		ORDER BY COALESCE(last_recalled_at, created_at) DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("fetching recent memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (store *GraphStore) TopK(queryEmbedding []float32, k int) ([]Memory, error) {
	queryJSON, err := json.Marshal(queryEmbedding)
	if err != nil {
		return nil, fmt.Errorf("marshaling query embedding: %w", err)
	}
	rows, err := store.database.Query(`
		SELECT m.id, m.type, m.title, m.content, m.recall_count, m.expires_at, m.created_at, m.last_recalled_at
		FROM vec_memories v
		JOIN memories m ON m.id = v.id
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance
	`, string(queryJSON), k)
	if err != nil {
		return nil, fmt.Errorf("executing similarity search: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	var memories []Memory
	for rows.Next() {
		var m Memory
		var expiresAt, lastRecalledAt sql.NullString
		if err := rows.Scan(&m.ID, &m.Type, &m.Title, &m.Content, &m.RecallCount, &expiresAt, &m.CreatedAt, &lastRecalledAt); err != nil {
			return nil, fmt.Errorf("scanning memory row: %w", err)
		}
		m.ExpiresAt = parseNullableTime(expiresAt)
		m.LastRecalledAt = parseNullableTime(lastRecalledAt)
		memories = append(memories, m)
	}
	if memories == nil {
		memories = []Memory{}
	}
	return memories, rows.Err()
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if parsed, err := time.Parse(layout, value.String); err == nil {
			return &parsed
		}
	}
	return nil
}

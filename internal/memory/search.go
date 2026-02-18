package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var registerOnce sync.Once

func ensureVecRegistered() {
	registerOnce.Do(registerVecExtension)
}

type SearchResult struct {
	Subject  string  `json:"subject"`
	FilePath string  `json:"filePath"`
	Storage  string  `json:"storage"`
	Distance float64 `json:"distance"`
}

type SearchIndex struct {
	database *sql.DB
}

func NewSearchIndex(databasePath string) (*SearchIndex, error) {
	ensureVecRegistered()
	database, err := sql.Open("sqlite3", databasePath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("opening search database: %w", err)
	}
	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if err := checkIntegrity(database); err != nil {
		database.Close()
		return nil, err
	}
	if err := createSchema(database); err != nil {
		database.Close()
		return nil, err
	}
	return &SearchIndex{database: database}, nil
}

func checkIntegrity(database *sql.DB) error {
	var result string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("running integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database integrity check failed: %s (memory files are preserved, but search index may need to be rebuilt)", result)
	}
	return nil
}

func (index *SearchIndex) Close() error {
	return index.database.Close()
}

func createSchema(database *sql.DB) error {
	metadataSchema := `CREATE TABLE IF NOT EXISTS memory_metadata (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		subject TEXT UNIQUE NOT NULL,
		file_path TEXT NOT NULL,
		storage TEXT NOT NULL DEFAULT 'short-term',
		recall_count INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_recalled_at DATETIME
	)`
	if _, err := database.Exec(metadataSchema); err != nil {
		return fmt.Errorf("creating memory_metadata table: %w", err)
	}
	vectorSchema := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_memories USING vec0(
		rowid INTEGER PRIMARY KEY,
		embedding float[%d]
	)`, EmbeddingDimension)
	if _, err := database.Exec(vectorSchema); err != nil {
		return fmt.Errorf("creating vec_memories table: %w", err)
	}
	return nil
}

func (index *SearchIndex) Upsert(subject string, filePath string, storage string, embedding []float32) error {
	transaction, err := index.database.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer transaction.Rollback()
	var metadataID int64
	err = transaction.QueryRow(
		"SELECT id FROM memory_metadata WHERE subject = ?", subject,
	).Scan(&metadataID)
	if err == sql.ErrNoRows {
		result, err := transaction.Exec(
			"INSERT INTO memory_metadata (subject, file_path, storage) VALUES (?, ?, ?)",
			subject, filePath, storage,
		)
		if err != nil {
			return fmt.Errorf("inserting metadata: %w", err)
		}
		metadataID, _ = result.LastInsertId()
		embeddingJSON, _ := json.Marshal(embedding)
		if _, err := transaction.Exec(
			"INSERT INTO vec_memories (rowid, embedding) VALUES (?, ?)",
			metadataID, string(embeddingJSON),
		); err != nil {
			return fmt.Errorf("inserting embedding: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("querying metadata: %w", err)
	} else {
		if _, err := transaction.Exec(
			"UPDATE memory_metadata SET file_path = ?, storage = ? WHERE id = ?",
			filePath, storage, metadataID,
		); err != nil {
			return fmt.Errorf("updating metadata: %w", err)
		}
		embeddingJSON, _ := json.Marshal(embedding)
		if _, err := transaction.Exec(
			"UPDATE vec_memories SET embedding = ? WHERE rowid = ?",
			string(embeddingJSON), metadataID,
		); err != nil {
			return fmt.Errorf("updating embedding: %w", err)
		}
	}
	return transaction.Commit()
}

func (index *SearchIndex) TopK(queryEmbedding []float32, limit int) ([]SearchResult, error) {
	queryJSON, _ := json.Marshal(queryEmbedding)
	rows, err := index.database.Query(`
		SELECT m.subject, m.file_path, m.storage, v.distance
		FROM vec_memories v
		JOIN memory_metadata m ON m.id = v.rowid
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance
	`, string(queryJSON), limit)
	if err != nil {
		return nil, fmt.Errorf("executing similarity search: %w", err)
	}
	defer rows.Close()
	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.Subject, &result.FilePath, &result.Storage, &result.Distance); err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}
		results = append(results, result)
	}
	if results == nil {
		results = []SearchResult{}
	}
	return results, rows.Err()
}

func (index *SearchIndex) UpdateRecallCount(subject string, recallCount int) error {
	_, err := index.database.Exec(
		"UPDATE memory_metadata SET recall_count = ?, last_recalled_at = CURRENT_TIMESTAMP WHERE subject = ?",
		recallCount, subject,
	)
	return err
}

func (index *SearchIndex) UpdateStorage(subject string, newStorage string, newFilePath string) error {
	_, err := index.database.Exec(
		"UPDATE memory_metadata SET storage = ?, file_path = ? WHERE subject = ?",
		newStorage, newFilePath, subject,
	)
	return err
}

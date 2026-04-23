package postgres

import "blueclaw/internal/task"

type TaskArtifactRepository struct {
	database Database
}

func NewTaskArtifactRepository(database Database) TaskArtifactRepository {
	return TaskArtifactRepository{database: database}
}

func (taskArtifactRepository TaskArtifactRepository) InsertTaskArtifact(taskArtifact task.TaskArtifact) error {
	_ = taskArtifact
	return nil
}

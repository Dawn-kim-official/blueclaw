package postgres

import (
	"context"
	"database/sql"

	"github.com/Dawn-kim-official/blueclaw/internal/task"
)

type TaskStepRepository struct {
	database Database
}

func NewTaskStepRepository(database Database) TaskStepRepository {
	return TaskStepRepository{database: database}
}

func (taskStepRepository TaskStepRepository) InsertTaskStep(taskStep task.TaskStep) error {
	_, errorValue := taskStepRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_step (task_step_id, task_run_id, parent_task_step_id, assigned_agent_profile_name, instruction, status, output)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (task_step_id) DO UPDATE SET
  status = EXCLUDED.status,
  output = EXCLUDED.output`,
		taskStep.TaskStepID,
		taskStep.TaskRunID,
		emptyStringAsNil(taskStep.ParentTaskStepID),
		taskStep.AssignedAgentProfileName,
		taskStep.Instruction,
		string(taskStep.Status),
		taskStep.Output,
	)
	return errorValue
}

func (taskStepRepository TaskStepRepository) ListTaskStep(taskRunID string) ([]task.TaskStep, error) {
	rows, errorValue := taskStepRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_step_id, task_run_id, COALESCE(parent_task_step_id, ''), assigned_agent_profile_name, instruction, status, COALESCE(output, '')
FROM task_step WHERE task_run_id = $1 ORDER BY task_step_id ASC`, taskRunID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskSteps(rows)
}

func scanTaskSteps(rows *sql.Rows) ([]task.TaskStep, error) {
	taskSteps := []task.TaskStep{}
	for rows.Next() {
		var taskStep task.TaskStep
		var status string
		if errorValue := rows.Scan(&taskStep.TaskStepID, &taskStep.TaskRunID, &taskStep.ParentTaskStepID, &taskStep.AssignedAgentProfileName, &taskStep.Instruction, &status, &taskStep.Output); errorValue != nil {
			return nil, errorValue
		}
		taskStep.Status = task.TaskStatus(status)
		taskSteps = append(taskSteps, taskStep)
	}
	return taskSteps, rows.Err()
}

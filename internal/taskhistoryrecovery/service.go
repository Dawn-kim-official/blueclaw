package taskhistoryrecovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"blueclaw/internal/store/postgres"
)

type Service struct{}

func (Service) Recover(ctx context.Context, sourceDatabase postgres.Database, targetDatabase postgres.Database, options Options) (Plan, error) {
	options = normalizeOptions(options)
	if errorValue := validateInputs(sourceDatabase, targetDatabase, options); errorValue != nil {
		return Plan{}, errorValue
	}

	sourceTransaction, errorValue := sourceDatabase.SQL.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if errorValue != nil {
		return Plan{}, fmt.Errorf("begin source snapshot transaction: %w", errorValue)
	}
	defer sourceTransaction.Rollback()
	if errorValue := configureCanonicalSession(ctx, sourceTransaction); errorValue != nil {
		return Plan{}, fmt.Errorf("configure source snapshot session: %w", errorValue)
	}

	snapshot, errorValue := loadSourceSnapshot(ctx, sourceTransaction)
	if errorValue != nil {
		return Plan{}, errorValue
	}

	targetTransaction, errorValue := beginTargetTransaction(ctx, targetDatabase.SQL, options.Apply)
	if errorValue != nil {
		return Plan{}, errorValue
	}
	defer targetTransaction.Rollback()
	if errorValue := configureCanonicalSession(ctx, targetTransaction); errorValue != nil {
		return Plan{}, fmt.Errorf("configure target recovery session: %w", errorValue)
	}

	if options.Apply {
		if errorValue := lockRecoveryTables(ctx, targetTransaction, options.LockTimeout); errorValue != nil {
			return Plan{}, errorValue
		}
	}
	targetIdentity, errorValue := loadDatabaseIdentity(ctx, targetTransaction)
	if errorValue != nil {
		return Plan{}, fmt.Errorf("identify target database: %w", errorValue)
	}
	if snapshot.DatabaseIdentity == targetIdentity {
		return Plan{}, errors.New("source and target resolve to the same PostgreSQL database")
	}
	if errorValue := validateTargetSchemas(ctx, targetTransaction, snapshot.Schemas); errorValue != nil {
		return Plan{}, errorValue
	}

	target, errorValue := inspectTargetState(ctx, targetTransaction, snapshot, targetIdentity)
	if errorValue != nil {
		return Plan{}, errorValue
	}
	planState := buildRecoveryPlan(snapshot, target, options)
	if !options.Apply {
		if errorValue := targetTransaction.Commit(); errorValue != nil {
			return Plan{}, fmt.Errorf("finish target dry-run transaction: %w", errorValue)
		}
		if errorValue := sourceTransaction.Commit(); errorValue != nil {
			return Plan{}, fmt.Errorf("finish source snapshot transaction: %w", errorValue)
		}
		return planState.Plan, nil
	}
	if errorValue := validatePlanForApply(planState.Plan); errorValue != nil {
		return planState.Plan, errorValue
	}

	insertedCounts, errorValue := insertRecoveryRows(ctx, targetTransaction, snapshot.Schemas, planState.ExpectedRows)
	if errorValue != nil {
		return planState.Plan, errorValue
	}
	planState.Plan.InsertedCounts = insertedCounts
	planState.Plan.Applied = true
	if errorValue := sourceTransaction.Commit(); errorValue != nil {
		planState.Plan.Applied = false
		planState.Plan.InsertedCounts = emptyTableCounts()
		return planState.Plan, fmt.Errorf("finish source snapshot before target commit: %w", errorValue)
	}
	if errorValue := targetTransaction.Commit(); errorValue != nil {
		planState.Plan.Applied = false
		planState.Plan.InsertedCounts = emptyTableCounts()
		return planState.Plan, fmt.Errorf("commit recovered task history: %w", errorValue)
	}
	return planState.Plan, nil
}

func validateInputs(sourceDatabase postgres.Database, targetDatabase postgres.Database, options Options) error {
	if sourceDatabase.SQL == nil {
		return errors.New("source PostgreSQL database is not open")
	}
	if targetDatabase.SQL == nil {
		return errors.New("target PostgreSQL database is not open")
	}
	if errorValue := ensureNoExcludedTables(); errorValue != nil {
		return errorValue
	}
	lockTimeout, errorValue := time.ParseDuration(options.LockTimeout)
	if errorValue != nil || lockTimeout <= 0 {
		return fmt.Errorf("lock timeout must be a positive duration: %q", options.LockTimeout)
	}
	return nil
}

func beginTargetTransaction(ctx context.Context, database *sql.DB, isApply bool) (*sql.Tx, error) {
	transactionOptions := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: !isApply}
	if isApply {
		transactionOptions.Isolation = sql.LevelSerializable
	}
	transaction, errorValue := database.BeginTx(ctx, transactionOptions)
	if errorValue != nil {
		return nil, fmt.Errorf("begin target recovery transaction: %w", errorValue)
	}
	return transaction, nil
}

func validatePlanForApply(plan Plan) error {
	unsafeReasons := []string{}
	if !plan.SafetyChecks.SchemasMatch {
		unsafeReasons = append(unsafeReasons, "schemas differ")
	}
	if !plan.SafetyChecks.DatabasesAreDistinct {
		unsafeReasons = append(unsafeReasons, "source and target are the same database")
	}
	if !plan.SafetyChecks.TargetAcceptsWrites {
		unsafeReasons = append(unsafeReasons, "target PostgreSQL server is in recovery")
	}
	if !plan.SafetyChecks.HasNoChildIdentifierConflicts {
		unsafeReasons = append(unsafeReasons, "target contains child row identifier conflicts")
	}
	if !plan.SafetyChecks.AllRequesterPersonsExist {
		unsafeReasons = append(unsafeReasons, "target is missing requester people")
	}
	if len(unsafeReasons) > 0 {
		return errors.New("refusing unsafe task history recovery: " + strings.Join(unsafeReasons, "; "))
	}
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"blueclaw/internal/store/postgres"
	"blueclaw/internal/taskhistoryrecovery"
)

type commandOptions struct {
	SourceConnectionString string
	TargetConnectionString string
	Apply                  bool
	DryRun                 bool
	SampleLimit            int
	LockTimeout            string
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, output io.Writer, errorOutput io.Writer) int {
	options, errorValue := parseCommandOptions(arguments, errorOutput)
	if errorValue != nil {
		fmt.Fprintln(errorOutput, errorValue)
		return 2
	}
	sourceDatabase, errorValue := postgres.OpenDatabase(ctx, options.SourceConnectionString)
	if errorValue != nil {
		fmt.Fprintln(errorOutput, "open source PostgreSQL database:", errorValue)
		return 1
	}
	defer sourceDatabase.Close()
	targetDatabase, errorValue := postgres.OpenDatabase(ctx, options.TargetConnectionString)
	if errorValue != nil {
		fmt.Fprintln(errorOutput, "open target PostgreSQL database:", errorValue)
		return 1
	}
	defer targetDatabase.Close()

	plan, errorValue := (taskhistoryrecovery.Service{}).Recover(ctx, sourceDatabase, targetDatabase, taskhistoryrecovery.Options{
		Apply:       options.Apply,
		SampleLimit: options.SampleLimit,
		LockTimeout: options.LockTimeout,
	})
	if plan.Mode != "" {
		if encodingError := writePlan(output, plan); encodingError != nil {
			fmt.Fprintln(errorOutput, "write recovery plan:", encodingError)
			return 1
		}
	}
	if errorValue != nil {
		fmt.Fprintln(errorOutput, errorValue)
		return 1
	}
	return 0
}

func parseCommandOptions(arguments []string, errorOutput io.Writer) (commandOptions, error) {
	options := commandOptions{}
	flags := flag.NewFlagSet("blueclaw-recover-task-history", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	flags.StringVar(&options.SourceConnectionString, "source-dsn", os.Getenv("BLUECLAW_HISTORY_SOURCE_DSN"), "old PostgreSQL source DSN")
	flags.StringVar(&options.TargetConnectionString, "target-dsn", os.Getenv("BLUECLAW_HISTORY_TARGET_DSN"), "current PostgreSQL target DSN")
	flags.BoolVar(&options.Apply, "apply", false, "insert the planned rows in one target transaction")
	flags.BoolVar(&options.DryRun, "dry-run", false, "print the recovery plan without writing")
	flags.IntVar(&options.SampleLimit, "sample-limit", 10, "maximum identifiers shown per sample")
	flags.StringVar(&options.LockTimeout, "lock-timeout", "5s", "maximum target table lock wait during apply")
	if errorValue := flags.Parse(arguments); errorValue != nil {
		return commandOptions{}, errorValue
	}
	if flags.NArg() != 0 {
		return commandOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Apply && options.DryRun {
		return commandOptions{}, errors.New("--apply and --dry-run cannot be used together")
	}
	if strings.TrimSpace(options.SourceConnectionString) == "" {
		return commandOptions{}, errors.New("--source-dsn or BLUECLAW_HISTORY_SOURCE_DSN is required")
	}
	if strings.TrimSpace(options.TargetConnectionString) == "" {
		return commandOptions{}, errors.New("--target-dsn or BLUECLAW_HISTORY_TARGET_DSN is required")
	}
	if options.SampleLimit <= 0 {
		return commandOptions{}, errors.New("--sample-limit must be positive")
	}
	return options, nil
}

func writePlan(output io.Writer, plan taskhistoryrecovery.Plan) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

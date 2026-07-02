package agent

import (
	"encoding/json"
	"testing"
)

func TestSitePublishPrerequisiteRejectsPublishAfterCreateWithoutBuild(t *testing.T) {
	observation, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{newContentObservation("obs-001", "continue", "site.create", `{"siteID":"site-1"}`)},
		sitePublishActionDocument(),
		"obs-002",
	)

	if !isRejected {
		t.Fatal("expected site.publish to be rejected until the site is built")
	}
	if observation.Tool != "site.publish" {
		t.Fatalf("expected site.publish failure observation, got %q", observation.Tool)
	}
	if observation.Failure == nil || len(observation.Failure.RequiredPreconditions) != 1 || observation.Failure.RequiredPreconditions[0] != siteBuiltRecoveryPrecondition {
		t.Fatalf("expected site_built precondition, got %+v", observation.Failure)
	}
}

func TestSitePublishPrerequisiteAllowsPublishAfterBuild(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.create", `{"siteID":"site-1"}`),
			siteBuildObservation("obs-002", "home/sites/site-1/draft/app", "bun scripts/build.ts"),
		},
		sitePublishActionDocument(),
		"obs-003",
	)

	if isRejected {
		t.Fatal("expected site.publish to be allowed after a successful site build")
	}
}

func TestSitePublishPrerequisiteAllowsPublishAfterStaticOutputBuild(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.create", `{"siteID":"site-1"}`),
			siteBuildObservation("obs-002", "home/sites/site-1/draft/app", "mkdir -p dist && printf '<!doctype html>' > dist/index.html"),
		},
		sitePublishActionDocument(),
		"obs-003",
	)

	if isRejected {
		t.Fatal("expected site.publish to be allowed after a successful static build output command")
	}
}

func TestSitePublishPrerequisiteRequiresRebuildAfterSourceChange(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.create", `{"siteID":"site-1"}`),
			siteBuildObservation("obs-002", "home/sites/site-1/draft/app", "bun scripts/build.ts"),
			newContentObservation("obs-003", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
		},
		sitePublishActionDocument(),
		"obs-004",
	)

	if !isRejected {
		t.Fatal("expected site.publish to be rejected after a newer site source change")
	}
}

func TestSitePublishPrerequisiteAllowsPublishWithoutObservedSourceChange(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(nil, sitePublishActionDocument(), "obs-001")

	if isRejected {
		t.Fatal("expected site.publish to be allowed when the current task has no observed source change")
	}
}

func TestSiteBuiltRecoveryPreconditionRequiresSiteBuildCommand(t *testing.T) {
	failedPublish, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{newContentObservation("obs-001", "continue", "site.create", `{"siteID":"site-1"}`)},
		sitePublishActionDocument(),
		"obs-002",
	)
	if !isRejected {
		t.Fatal("expected missing build rejection")
	}

	grepOnly := siteBuildObservation("obs-003", "home/sites/site-1/draft/app", "grep -q Local dist/index.html")
	if len(missingRecoveryPreconditions(failedPublish, []turnObservation{failedPublish, grepOnly})) == 0 {
		t.Fatal("expected grep-only terminal.run not to satisfy site_built")
	}

	build := siteBuildObservation("obs-004", "home/sites/site-1/draft/app", "bun scripts/build.ts")
	if missing := missingRecoveryPreconditions(failedPublish, []turnObservation{failedPublish, build}); len(missing) != 0 {
		t.Fatalf("expected site build to satisfy precondition, got %+v", missing)
	}
}

func sitePublishActionDocument() turnActionDocument {
	return turnActionDocument{
		ToolName:  CapabilityInvokeToolName,
		ToolInput: json.RawMessage(`{"operation":"site.publish","input":{"siteID":"site-1"}}`),
	}
}

func siteBuildObservation(observationID string, workingDirectoryPath string, command string) turnObservation {
	input, errorValue := json.Marshal(map[string]string{
		"workingDirectoryPath": workingDirectoryPath,
		"command":              command,
	})
	if errorValue != nil {
		panic(errorValue)
	}
	observation := newContentObservation(observationID, "continue", "terminal.run", `{"exitCode":0}`)
	observation.ToolInputKey = canonicalToolCallKey("terminal.run", input)
	return observation
}

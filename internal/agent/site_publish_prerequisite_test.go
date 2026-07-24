package agent

import (
	"encoding/json"
	"testing"
)

func TestSitePublishPrerequisiteAllowsPublishAfterCreateWithoutBuild(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{newContentObservation("obs-001", "continue", "site.list", `{}`)},
		sitePublishActionDocument(),
		"obs-002",
	)

	if isRejected {
		t.Fatal("expected site.list alone not to require a rebuild before serve")
	}
}

func TestSitePublishPrerequisiteRejectsPublishAfterAppSourceChangeWithoutBuild(t *testing.T) {
	observation, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
		},
		sitePublishActionDocument(),
		"obs-003",
	)

	if !isRejected {
		t.Fatal("expected site.serve to be rejected until the site is rebuilt")
	}
	if observation.Tool != "site.serve" {
		t.Fatalf("expected site.serve failure observation, got %q", observation.Tool)
	}
	if observation.Failure == nil || len(observation.Failure.RequiredPreconditions) != 1 || observation.Failure.RequiredPreconditions[0] != siteBuiltRecoveryPrecondition {
		t.Fatalf("expected site_built precondition, got %+v", observation.Failure)
	}
}

func TestSitePublishPrerequisiteAllowsPublishAfterContentOnlyChange(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/public/site-content.json"}`),
		},
		sitePublishActionDocument(),
		"obs-003",
	)

	if isRejected {
		t.Fatal("expected site.serve to be allowed after a content-only change under app/public/")
	}
}

func TestSitePublishPrerequisiteAllowsPublishAfterDesignOrControlFileChange(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/DESIGN.md"}`),
		},
		sitePublishActionDocument(),
		"obs-003",
	)

	if isRejected {
		t.Fatal("expected site.serve to be allowed after a DESIGN.md change")
	}

	_, isRejectedForControlFile := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/.internkim/notes.json"}`),
		},
		sitePublishActionDocument(),
		"obs-003",
	)

	if isRejectedForControlFile {
		t.Fatal("expected site.serve to be allowed after a .internkim/ control file change")
	}
}

func TestSitePublishPrerequisiteAllowsPublishAfterBuild(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
			siteBuildObservation("obs-003", "home/sites/site-1/draft/app", "bun scripts/build.ts"),
		},
		sitePublishActionDocument(),
		"obs-004",
	)

	if isRejected {
		t.Fatal("expected site.serve to be allowed after a successful site build")
	}
}

func TestSitePublishPrerequisiteAllowsPublishAfterStaticOutputBuild(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
			siteBuildObservation("obs-003", "home/sites/site-1/draft/app", "mkdir -p dist && printf '<!doctype html>' > dist/index.html"),
		},
		sitePublishActionDocument(),
		"obs-004",
	)

	if isRejected {
		t.Fatal("expected site.serve to be allowed after a successful static build output command")
	}
}

func TestSitePublishPrerequisiteRequiresRebuildAfterSourceChange(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
			siteBuildObservation("obs-003", "home/sites/site-1/draft/app", "bun scripts/build.ts"),
			newContentObservation("obs-004", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
		},
		sitePublishActionDocument(),
		"obs-005",
	)

	if !isRejected {
		t.Fatal("expected site.serve to be rejected after a newer site source change")
	}
}

func TestSitePublishPrerequisiteAllowsPublishWithoutObservedSourceChange(t *testing.T) {
	_, isRejected := sitePublishPrerequisiteFailure(nil, sitePublishActionDocument(), "obs-001")

	if isRejected {
		t.Fatal("expected site.serve to be allowed when the current task has no observed source change")
	}
}

func TestSiteBuiltRecoveryPreconditionRequiresSiteBuildCommand(t *testing.T) {
	failedPublish, isRejected := sitePublishPrerequisiteFailure(
		[]turnObservation{
			newContentObservation("obs-001", "continue", "site.list", `{}`),
			newContentObservation("obs-002", "continue", FileWriteToolName, `{"path":"home/sites/site-1/draft/app/src/App.tsx"}`),
		},
		sitePublishActionDocument(),
		"obs-003",
	)
	if !isRejected {
		t.Fatal("expected missing build rejection")
	}

	grepOnly := siteBuildObservation("obs-004", "home/sites/site-1/draft/app", "grep -q Local dist/index.html")
	if len(missingRecoveryPreconditions(failedPublish, []turnObservation{failedPublish, grepOnly})) == 0 {
		t.Fatal("expected grep-only terminal.run not to satisfy site_built")
	}

	build := siteBuildObservation("obs-005", "home/sites/site-1/draft/app", "bun scripts/build.ts")
	if missing := missingRecoveryPreconditions(failedPublish, []turnObservation{failedPublish, build}); len(missing) != 0 {
		t.Fatalf("expected site build to satisfy precondition, got %+v", missing)
	}
}

func sitePublishActionDocument() turnActionDocument {
	return turnActionDocument{ToolName: "site.serve", ToolInput: json.RawMessage(`{"title":"Site 1","sourceWorkspacePath":"home/sites/site-1/draft","mode":"publish"}`)}
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

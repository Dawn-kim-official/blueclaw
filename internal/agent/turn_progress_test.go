package agent

import (
	"strings"
	"testing"
)

func TestSiteAppCreateSummaryKeepsAgentWorkspacePaths(t *testing.T) {
	summary := summarizeObservationContent(turnObservation{
		Action: "call_tool",
		Tool:   "site.app.create",
		Output: ToolOutput{Content: `{
			"siteID":"site-1",
			"slug":"demo",
			"workspacePath":"home/sites/site-1",
			"sourceWorkspacePath":"home/sites/site-1",
			"hostSourcePath":"/root/.blueclaw/workspace/sites/site-1",
			"status":"draft"
		}`},
	})

	if !strings.Contains(summary, "sourceWorkspacePath=home/sites/site-1") {
		t.Fatalf("expected sourceWorkspacePath in summary, got %q", summary)
	}
	if strings.Contains(summary, "hostSourcePath") || strings.Contains(summary, "/root/.blueclaw") {
		t.Fatalf("expected host path to stay hidden, got %q", summary)
	}
}

func TestWorkspacePathSummaryHidesNonAgentWorkspacePath(t *testing.T) {
	summary := summarizeObservationContent(turnObservation{
		Action: "call_tool",
		Tool:   "site.app.create",
		Output: ToolOutput{Content: `{
			"siteID":"site-1",
			"workspacePath":"/root/.blueclaw/workspace/sites/site-1",
			"sourceWorkspacePath":"/root/.blueclaw/workspace/sites/site-1"
		}`},
	})

	if strings.Contains(summary, "/root/.blueclaw") {
		t.Fatalf("expected non-agent workspace path to stay hidden, got %q", summary)
	}
}

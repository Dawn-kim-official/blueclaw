package bluecollar

import (
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"strings"
	"testing"
)

func TestBuildTurnProgressKeepsRecentFileContext(t *testing.T) {
	observations := []turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "file_read",
			Output:        toolcontract.ToolOutput{Content: `{"path":"home/sites/site-1/draft/app/src/prototype-data.ts","content":"export const profile = { name: \"Kim Intern\" }\nexport const services = [{ title: \"Mail\" }, { title: \"Slides\" }]","startLine":1,"endLine":20,"totalLines":80,"sizeBytes":2048,"isTruncated":false}`},
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "file_read",
			Output:        toolcontract.ToolOutput{Content: `{"path":"home/sites/site-1/draft/app/src/prototype-data.ts","content":"export const profile = { name: \"Kim Intern\" }\nexport const services = [{ title: \"Mail\" }, { title: \"Slides\" }]","startLine":1,"endLine":20,"totalLines":80,"sizeBytes":2048,"isTruncated":false}`},
		},
	}

	progress := buildTurnProgress(AgentTurnRequest{}, observations)

	if len(progress.RecentFiles) != 1 {
		t.Fatalf("expected one recent file context, got %+v", progress.RecentFiles)
	}
	fileContext := progress.RecentFiles[0]
	if fileContext.Path != "home/sites/site-1/draft/app/src/prototype-data.ts" {
		t.Fatalf("expected prototype data path, got %q", fileContext.Path)
	}
	for _, expected := range []string{"profile", "services", "1-20"} {
		if !strings.Contains(fileContext.Summary+strings.Join(fileContext.ReadRanges, ","), expected) {
			t.Fatalf("expected recent file context to include %q, got %+v", expected, fileContext)
		}
	}
	if !strings.Contains(fileContext.Snippet, "export const profile") {
		t.Fatalf("expected exact snippet to remain available, got %+v", fileContext)
	}
	if !strings.Contains(fileContext.RepeatedReadGuidance, "edit/build") {
		t.Fatalf("expected repeated read guidance, got %+v", fileContext)
	}
}

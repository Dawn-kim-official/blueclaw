package task

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var eventNamesWrittenOnBothSides = map[string]string{
	"approval.pending_call":       "the host gate, once the loop stops holding calls of its own",
	"confirmation.requested":      "the host gate, once the loop stops holding calls of its own",
	"ask.requested":               "the host gate, once the loop stops holding calls of its own",
	"agent.failure_reply":         "the host, which sees every turn result",
	"agent.failure_report":        "the host, which sees every turn result",
	"agent.limit_reply":           "the host, which sees every turn result",
	"agent.limit_stop":            "the host, which sees every turn result",
	"agent.goal.blocked":          "the host, which sees every turn result",
	"task.stop.outbox_suppressed": "the host, which is what cancelled the run",
	"llm.call":                    "undecided: the host records the calls it makes and the agent records its own, which may be two events of one kind rather than one event with two writers",
}

var taskEventWriteCall = regexp.MustCompile(`(?s:(?:AppendTaskEvent|appendEvent|appendTaskEvent)\(\s*[^,]+,\s*"([a-z0-9_.]+)")`)

func eventNamesWrittenUnder(t *testing.T, rootPath string) map[string]bool {
	t.Helper()
	writtenNames := map[string]bool{}
	errorValue := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readError := os.ReadFile(path)
		if readError != nil {
			return readError
		}
		for _, match := range taskEventWriteCall.FindAllStringSubmatch(string(source), -1) {
			writtenNames[match[1]] = true
		}
		return nil
	})
	if errorValue != nil {
		t.Fatalf("walking %s: %v", rootPath, errorValue)
	}
	return writtenNames
}

func TestNoNewEventNameGainsASecondWriter(t *testing.T) {
	hostNames := eventNamesWrittenUnder(t, "../../internal")
	for name := range eventNamesWrittenUnder(t, "../../cmd") {
		hostNames[name] = true
	}
	loopNames := eventNamesWrittenUnder(t, "../../.dependency/bluecollar")

	writtenOnBothSides := []string{}
	for name := range hostNames {
		if loopNames[name] {
			writtenOnBothSides = append(writtenOnBothSides, name)
		}
	}
	sort.Strings(writtenOnBothSides)

	for _, name := range writtenOnBothSides {
		if _, isKnown := eventNamesWrittenOnBothSides[name]; !isKnown {
			t.Fatalf("%q is now written by the host and by the agent loop, so which one fires depends on the configured harness; give it one owner or add it to eventNamesWrittenOnBothSides with where that owner will be", name)
		}
	}
	for name := range eventNamesWrittenOnBothSides {
		if hostNames[name] && loopNames[name] {
			continue
		}
		t.Fatalf("%q has one writer again; drop it from eventNamesWrittenOnBothSides", name)
	}
}

package agent

import "testing"

func TestRegisteredToolNamesOnlyKeepsRegisteredAndDropsUnknown(t *testing.T) {
	toolSet := testToolSet([]string{"flow.task.add", "flow.task.list"})

	filtered := registeredToolNamesOnly(toolSet, []string{"flow.task.add", "made.up.tool", "flow.task.add", "flow.task.list"})

	if len(filtered) != 2 {
		t.Fatalf("expected only registered unique tools, got %+v", filtered)
	}
	if !containsString(filtered, "flow.task.add") || !containsString(filtered, "flow.task.list") {
		t.Fatalf("expected registered tools retained, got %+v", filtered)
	}
	if containsString(filtered, "made.up.tool") {
		t.Fatalf("expected unregistered tool dropped, got %+v", filtered)
	}
}

func TestRegisteredToolNamesOnlyEmptyInputsReturnNil(t *testing.T) {
	if registeredToolNamesOnly(nil, []string{"flow.task.add"}) != nil {
		t.Fatal("expected nil for nil tool set")
	}
	if registeredToolNamesOnly(testToolSet([]string{"flow.task.add"}), nil) != nil {
		t.Fatal("expected nil for empty names")
	}
}

package bluecollar

import "testing"

func TestRegisteredToolNamesOnlyKeepsRegisteredAndDropsUnknown(t *testing.T) {
	toolSet := testToolSet([]string{"task.add", "task.list"})

	filtered := registeredToolNamesOnly(toolSet, []string{"task.add", "made.up.tool", "task.add", "task.list"})

	if len(filtered) != 2 {
		t.Fatalf("expected only registered unique tools, got %+v", filtered)
	}
	if !containsString(filtered, "task.add") || !containsString(filtered, "task.list") {
		t.Fatalf("expected registered tools retained, got %+v", filtered)
	}
	if containsString(filtered, "made.up.tool") {
		t.Fatalf("expected unregistered tool dropped, got %+v", filtered)
	}
}

func TestRegisteredToolNamesOnlyEmptyInputsReturnNil(t *testing.T) {
	if registeredToolNamesOnly(nil, []string{"task.add"}) != nil {
		t.Fatal("expected nil for nil tool set")
	}
	if registeredToolNamesOnly(testToolSet([]string{"task.add"}), nil) != nil {
		t.Fatal("expected nil for empty names")
	}
}

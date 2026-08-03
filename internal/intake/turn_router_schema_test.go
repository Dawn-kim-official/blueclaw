package intake

import (
	"strconv"
	"strings"
	"testing"
)

func namesOfCount(count int) []string {
	names := make([]string, 0, count)
	for index := 0; index < count; index++ {
		names = append(names, "tool_"+strconv.Itoa(index))
	}
	return names
}

func TestAShortToolListStaysAnEnumTheModelMustObey(t *testing.T) {
	schema := boundedNamedStringArraySchema(namesOfCount(5))

	itemSchema, _ := schema["items"].(map[string]any)
	if _, hasEnum := itemSchema["enum"]; !hasEnum {
		t.Fatal("expected a short list to stay an enum, because that is what keeps the model to real tool names")
	}
}

func TestALongToolListDropsTheEnumProvidersRefuse(t *testing.T) {
	names := namesOfCount(namedStringEnumLimit + 1)

	schema := boundedNamedStringArraySchema(names)

	itemSchema, _ := schema["items"].(map[string]any)
	if _, hasEnum := itemSchema["enum"]; hasEnum {
		t.Fatalf("expected an enum this long to be dropped, because a provider that refuses it fails every request at routing")
	}
	description, _ := itemSchema["description"].(string)
	if !strings.Contains(description, names[0]) || !strings.Contains(description, names[len(names)-1]) {
		t.Fatal("expected every name to still be offered in the description, so the model can still pick a real one")
	}
}

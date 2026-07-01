package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityCatalogHealsNameOnlyDescriptorFromLiveSchema(t *testing.T) {
	builder := NewToolCatalogBuilder()
	builder.capabilityToolNames = []string{"calendar.add"}

	entriesBefore := builder.capabilityCatalogEntries()
	if len(entriesBefore) != 1 {
		t.Fatalf("expected one catalog entry, got %v", entriesBefore)
	}
	if strings.Contains(entriesBefore[0], "input:") {
		t.Fatalf("a name-only descriptor must carry no field hints (this is the empty-input bug precondition), got %q", entriesBefore[0])
	}

	builder.liveCapabilityInputSchema = map[string]json.RawMessage{
		"calendar.add": json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"startISO":{"type":"string"},"endISO":{"type":"string"},"timeZone":{"type":"string"}},"required":["title","startISO","endISO"]}`),
	}

	entriesAfter := builder.capabilityCatalogEntries()
	if len(entriesAfter) != 1 {
		t.Fatalf("expected one catalog entry, got %v", entriesAfter)
	}
	wantHint := "input: endISO (required), startISO (required), title (required), timeZone"
	if !strings.Contains(entriesAfter[0], wantHint) {
		t.Fatalf("stale name-only descriptor should be healed from the live schema; want substring %q, got %q", wantHint, entriesAfter[0])
	}
}

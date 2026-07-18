package capabilitycatalog

import (
	"encoding/json"
	"testing"
)

func TestCapabilityToolCatalogReturnsIndependentValidDocuments(t *testing.T) {
	firstDocument := CapabilityToolCatalog()
	secondDocument := CapabilityToolCatalog()
	if len(firstDocument) == 0 {
		t.Fatal("expected embedded capability tool catalog")
	}
	firstDocument[0] = 0
	if secondDocument[0] == 0 {
		t.Fatal("expected independent catalog documents")
	}
	var catalog struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if errorValue := json.Unmarshal(secondDocument, &catalog); errorValue != nil || len(catalog.Tools) == 0 {
		t.Fatalf("expected valid embedded capability tool catalog: %v", errorValue)
	}
}

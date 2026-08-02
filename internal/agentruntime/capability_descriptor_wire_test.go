package agentruntime

import (
	"encoding/json"
	"testing"
)

// The companion gate reads RequiresRequesterDevice. If the wire name ever stops
// landing in this field it silently reads false, and every browser tool becomes
// reachable with no companion connected.

func TestCapabilityDescriptorCarriesTheCompanionBrowserAxisFromTheWire(t *testing.T) {
	var descriptor CapabilityToolDescriptor
	if errorValue := json.Unmarshal([]byte(`{"name":"browser_open","namespace":"browser","requiresRequesterDevice":true}`), &descriptor); errorValue != nil {
		t.Fatalf("expected the registry descriptor to decode, got %v", errorValue)
	}
	if !descriptor.RequiresRequesterDevice {
		t.Fatal("expected requiresRequesterDevice to reach the descriptor the companion gate reads")
	}
	if !descriptorIsBrowserCapability(descriptor) {
		t.Fatal("expected the declared axis to make the tool a companion browser capability")
	}
}

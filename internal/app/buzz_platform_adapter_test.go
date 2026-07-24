package app

import (
	"testing"

	"blueclaw/internal/config"
	"blueclaw/internal/connectors"
)

func TestNewAcpdClientDefaultsEndpointWhenUnconfigured(t *testing.T) {
	client := newAcpdClient(config.RuntimeConfiguration{})

	if client.Endpoint != connectors.DefaultAcpdEndpoint {
		t.Fatalf("expected default acpd endpoint, got %q", client.Endpoint)
	}
}

func TestNewAcpdClientUsesConfiguredEndpoint(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Connectors.Buzz.Endpoint = "http://acpd.test"

	client := newAcpdClient(runtimeConfiguration)

	if client.Endpoint != "http://acpd.test" {
		t.Fatalf("expected configured acpd endpoint, got %q", client.Endpoint)
	}
}

func TestNewBuzzPlatformAdapterUsesChatdContractAgainstAcpd(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Connectors.Buzz.Endpoint = "http://acpd.test"

	adapter := newBuzzPlatformAdapter(runtimeConfiguration)

	chatdAdapter, isChatdAdapter := adapter.(connectors.ChatdPlatformAdapter)
	if !isChatdAdapter {
		t.Fatalf("expected chatd-contract adapter for buzz, got %T", adapter)
	}
	if chatdAdapter.Name() != "buzz" {
		t.Fatalf("expected buzz platform name, got %q", chatdAdapter.Name())
	}
	if chatdAdapter.ChatdClient.Endpoint != "http://acpd.test" {
		t.Fatalf("expected acpd endpoint on buzz adapter, got %q", chatdAdapter.ChatdClient.Endpoint)
	}
}

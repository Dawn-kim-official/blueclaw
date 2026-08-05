package app

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func TestBuzzPlatformUsesChatdContract(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Connectors.Chatd.Endpoint = "http://chatd.test"

	adapter := connectors.NewChatdPlatformAdapter("buzz", newChatdClient(runtimeConfiguration))

	if adapter.Name() != "buzz" {
		t.Fatalf("expected buzz platform name, got %q", adapter.Name())
	}
	if adapter.ChatdClient.Endpoint != "http://chatd.test" {
		t.Fatalf("expected chatd endpoint on buzz adapter, got %q", adapter.ChatdClient.Endpoint)
	}
}

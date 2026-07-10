package connectors

import "testing"

// A channel/thread message the user directs at the bot (an explicit mention)
// must show the typing indicator immediately, not only after the addressing
// classifier LLM resolves. Ambient, unaddressed channel chatter stays quiet
// until the addressing gate decides to engage.
func TestShouldStartProgressBeforeAddressing(t *testing.T) {
	directMessage := PlatformInboundEvent{Context: VisibleContext{ConversationType: "d"}}
	if !shouldStartProgressBeforeAddressing(directMessage) {
		t.Fatal("expected a direct message to start typing before addressing")
	}

	mentionedInChannel := PlatformInboundEvent{Context: VisibleContext{ConversationType: "O", Addressing: AddressingMetadata{BotMentioned: true}}}
	if !shouldStartProgressBeforeAddressing(mentionedInChannel) {
		t.Fatal("expected a channel mention to start typing before addressing")
	}

	ambientChannelChatter := PlatformInboundEvent{Context: VisibleContext{ConversationType: "O"}}
	if shouldStartProgressBeforeAddressing(ambientChannelChatter) {
		t.Fatal("expected ambient channel chatter to stay quiet until the addressing gate decides")
	}
}

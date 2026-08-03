package bluecollar

import (
	"strings"
	"testing"
)

func TestResponseLanguageInstructionKeepsEmojiOutOfMessageText(t *testing.T) {
	for _, responseLanguage := range []string{ResponseLanguageKorean, ResponseLanguageEnglish} {
		instruction := responseLanguageInstruction(responseLanguage)
		for _, fragment := range []string{"Do not put emoji in message text", "use message reactions"} {
			if !strings.Contains(instruction, fragment) {
				t.Fatalf("expected response language instruction for %q to contain %q, got %q", responseLanguage, fragment, instruction)
			}
		}
	}
}

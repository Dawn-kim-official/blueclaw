package memory

import (
	"fmt"
	"strings"
)

const RememberContentRuneLimit = 600

func RememberContentGateMessage(content string) string {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return "memory.remember requires content"
	}
	if len([]rune(trimmedContent)) > RememberContentRuneLimit {
		return fmt.Sprintf("memory.remember content exceeds %d characters; store one compact standalone fact per call", RememberContentRuneLimit)
	}
	return ""
}

package tui

import tea "charm.land/bubbletea/v2"

func typedText(keyPressMsg tea.KeyPressMsg) (string, bool) {
	keyName := keyPressMsg.String()
	if keyName == "space" {
		return " ", true
	}
	if len([]rune(keyName)) != 1 {
		return "", false
	}
	return keyName, true
}

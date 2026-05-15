package main

import "testing"

func TestModeTextIncludesSetGID(t *testing.T) {
	if !modeTextIncludesSetGID("2770") {
		t.Fatal("expected 2770 to include setgid")
	}
	if modeTextIncludesSetGID("0711") {
		t.Fatal("expected 0711 not to include setgid")
	}
	if modeTextIncludesSetGID("invalid") {
		t.Fatal("expected invalid mode to omit setgid")
	}
}

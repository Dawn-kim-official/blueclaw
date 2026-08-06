package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/yeomyeonggeori/blueclaw/internal/enrollment"
)

func TestEveryLogoLineIsTheSameWidth(testInstance *testing.T) {
	widths := map[int]int{}
	for _, line := range logoLines() {
		widths[lipgloss.Width(line)]++
	}

	if len(widths) != 1 {
		testInstance.Fatalf("expected one logo width so the art does not shear, got %v", widths)
	}
	if logoWidth() == 0 || logoHeight() == 0 {
		testInstance.Fatalf("expected a logo with real dimensions, got %dx%d", logoWidth(), logoHeight())
	}
}

func TestLogoKeepsEveryGlyphOfTheSourceArt(testInstance *testing.T) {
	expectedLines := blankMarginsRemoved(assetGlyphLines())
	if len(expectedLines) != logoHeight() {
		testInstance.Fatalf("expected every asset row to be rendered, got %d of %d", logoHeight(), len(expectedLines))
	}
	for lineIndex, expectedGlyphs := range expectedLines {
		renderedGlyphs := stripStyles(logoLines()[lineIndex])
		if renderedGlyphs != expectedGlyphs {
			testInstance.Fatalf("row %d lost its shape\n want %q\n  got %q", lineIndex, expectedGlyphs, renderedGlyphs)
		}
	}
}

func TestTheLogoCarriesNoBlankMargin(testInstance *testing.T) {
	lines := logoLines()

	hasGlyphInFirstColumn, hasGlyphInLastColumn := false, false
	for _, line := range lines {
		glyphs := []rune(stripStyles(line))
		hasGlyphInFirstColumn = hasGlyphInFirstColumn || glyphs[0] != ' '
		hasGlyphInLastColumn = hasGlyphInLastColumn || glyphs[len(glyphs)-1] != ' '
	}
	if !hasGlyphInFirstColumn || !hasGlyphInLastColumn {
		testInstance.Fatalf("expected the art to touch both edges, got a blank margin: left=%v right=%v",
			!hasGlyphInFirstColumn, !hasGlyphInLastColumn)
	}
}

func assetGlyphLines() []string {
	assetLines := strings.Split(strings.TrimRight(logoAsset, "\n"), "\n")
	glyphLines := make([]string, 0, len(assetLines))
	for _, assetLine := range assetLines {
		glyphLines = append(glyphLines, glyphsOfAssetLine(assetLine))
	}
	return glyphLines
}

func blankMarginsRemoved(glyphLines []string) []string {
	firstFilled, lastFilled := len(glyphLines[0]), -1
	for _, glyphLine := range glyphLines {
		for columnIndex, glyph := range glyphLine {
			if glyph == ' ' {
				continue
			}
			if columnIndex < firstFilled {
				firstFilled = columnIndex
			}
			if columnIndex > lastFilled {
				lastFilled = columnIndex
			}
		}
	}
	trimmed := make([]string, 0, len(glyphLines))
	for _, glyphLine := range glyphLines {
		trimmed = append(trimmed, glyphLine[firstFilled:lastFilled+1])
	}
	return trimmed
}

func TestTheClawRowIsLeftRightSymmetric(testInstance *testing.T) {
	const clawRowIndex = 7
	assetLines := strings.Split(strings.TrimRight(logoAsset, "\n"), "\n")
	glyphs := []rune(glyphsOfAssetLine(assetLines[clawRowIndex]))

	for columnIndex := range glyphs {
		mirrored := glyphs[len(glyphs)-1-columnIndex]
		if (glyphs[columnIndex] == ' ') != (mirrored == ' ') {
			testInstance.Fatalf("the claw row is lopsided at column %d: %q", columnIndex, string(glyphs))
		}
	}
}

func glyphsOfAssetLine(assetLine string) string {
	glyphs := strings.Builder{}
	for _, cell := range parseLogoCells(assetLine) {
		if cell.hexColor == logoBackgroundColor {
			glyphs.WriteString(" ")
			continue
		}
		glyphs.WriteString(cell.glyph)
	}
	return glyphs.String()
}

func stripStyles(line string) string {
	plain := strings.Builder{}
	isInsideEscape := false
	for _, character := range line {
		switch {
		case character == '\x1b':
			isInsideEscape = true
		case isInsideEscape && character == 'm':
			isInsideEscape = false
		case !isInsideEscape:
			plain.WriteRune(character)
		}
	}
	return plain.String()
}

func sizedModel(width int, height int) Model {
	model := NewModel(NewClient("http://127.0.0.1:8080", nil), "")
	resized, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return resized.(Model)
}

func TestTheLogoSitsAboveTheHeaderBar(testInstance *testing.T) {
	model := sizedModel(logoWidth()+20, logoHeight()+20)

	lines := strings.Split(stripStyles(model.View().Content), "\n")

	headerIndex := indexOfLineContaining(lines, "blueclaw · 127.0.0.1:8080")
	if headerIndex < 0 {
		testInstance.Fatal("expected the header bar to be rendered")
	}
	if headerIndex != logoHeight() {
		testInstance.Fatalf("expected the header directly below the %d logo rows, found it at line %d", logoHeight(), headerIndex)
	}
	for logoIndex, logoLine := range logoLines() {
		if strings.TrimRight(lines[logoIndex], " ") != strings.TrimRight(stripStyles(logoLine), " ") {
			testInstance.Fatalf("logo row %d is not above the header:\n want %q\n  got %q",
				logoIndex, stripStyles(logoLine), lines[logoIndex])
		}
	}
}

func TestTheTaskListStillFollowsTheLogoAndHeader(testInstance *testing.T) {
	model := sizedModel(logoWidth()+20, logoHeight()+20)

	lines := strings.Split(stripStyles(model.View().Content), "\n")

	if indexOfLineContaining(lines, "Tasks") <= logoHeight() {
		testInstance.Fatal("expected the tab bar under the logo")
	}
	if indexOfLineContaining(lines, "No task runs yet") < 0 {
		testInstance.Fatal("expected the task screen to keep rendering below the logo")
	}
}

func indexOfLineContaining(lines []string, text string) int {
	for lineIndex, line := range lines {
		if strings.Contains(line, text) {
			return lineIndex
		}
	}
	return -1
}

func TestATerminalTooSmallForTheLogoStillGetsTheWholeInterface(testInstance *testing.T) {
	narrow := sizedModel(logoWidth()-1, logoHeight()+20)
	short := sizedModel(logoWidth()+20, logoHeight()+minimumHeightBesidesLogo-1)

	for _, model := range []Model{narrow, short} {
		lines := strings.Split(stripStyles(model.View().Content), "\n")
		if indexOfLineContaining(lines, "blueclaw") != 0 {
			testInstance.Fatalf("expected the header on the first line when the logo does not fit, got %q", lines[0])
		}
		if indexOfLineContaining(lines, "1-4 screens") < 0 {
			testInstance.Fatal("expected the footer to survive on a small terminal")
		}
	}
}

func TestTheLogoDoesNotEatTheRowsTheTableIsGiven(testInstance *testing.T) {
	withLogo := sizedModel(logoWidth()+20, logoHeight()+20)
	withoutLogo := sizedModel(logoWidth()-1, logoHeight()+20)

	if withLogo.bodyHeight() != withoutLogo.bodyHeight()-logoHeight() {
		testInstance.Fatalf("expected the logo's rows to leave the body, got %d with and %d without",
			withLogo.bodyHeight(), withoutLogo.bodyHeight())
	}
}

func TestSetupShowsTheLogoAboveItsHeader(testInstance *testing.T) {
	setupModel := NewSetupModel(enrollment.Home{DirectoryPath: filepath.Join(testInstance.TempDir(), "blueclaw")})
	resized, _ := setupModel.Update(tea.WindowSizeMsg{Width: logoWidth() + 20, Height: logoHeight() + 20})

	lines := strings.Split(stripStyles(resized.(SetupModel).View().Content), "\n")

	if indexOfLineContaining(lines, "blueclaw setup") != logoHeight() {
		testInstance.Fatalf("expected the setup header directly below the logo, found it at line %d",
			indexOfLineContaining(lines, "blueclaw setup"))
	}
	if indexOfLineContaining(lines, "Nothing is configured yet") < 0 {
		testInstance.Fatal("expected the setup questions to stay on screen")
	}
}

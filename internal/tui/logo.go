package tui

import (
	_ "embed"
	"strings"
	"sync"

	lipgloss "charm.land/lipgloss/v2"
)

//go:embed assets/logo.txt
var logoAsset string

const (
	logoColorWidth           = 6
	logoCellWidth            = logoColorWidth + 1
	logoBackgroundColor      = "000000"
	minimumHeightBesidesLogo = 9
)

type logoCell struct {
	hexColor string
	glyph    string
}

var (
	loadLogoOnce sync.Once
	logoLineList []string
)

func logoLines() []string {
	loadLogoOnce.Do(func() { logoLineList = renderLogoAsset(logoAsset) })
	return logoLineList
}

func renderLogoAsset(asset string) []string {
	styleByColor := map[string]lipgloss.Style{}
	assetLines := strings.Split(strings.TrimRight(asset, "\n"), "\n")
	rows := make([][]logoCell, 0, len(assetLines))
	for _, assetLine := range assetLines {
		rows = append(rows, parseLogoCells(assetLine))
	}
	lines := make([]string, 0, len(rows))
	for _, row := range trimBlankColumns(rows) {
		lines = append(lines, renderLogoCells(row, styleByColor))
	}
	return lines
}

func trimBlankColumns(rows [][]logoCell) [][]logoCell {
	firstFilled, lastFilled := -1, -1
	for _, row := range rows {
		for columnIndex, cell := range row {
			if cell.hexColor == logoBackgroundColor {
				continue
			}
			if firstFilled < 0 || columnIndex < firstFilled {
				firstFilled = columnIndex
			}
			if columnIndex > lastFilled {
				lastFilled = columnIndex
			}
		}
	}
	if firstFilled < 0 {
		return rows
	}
	trimmed := make([][]logoCell, 0, len(rows))
	for _, row := range rows {
		trimmed = append(trimmed, row[firstFilled:lastFilled+1])
	}
	return trimmed
}

func parseLogoCells(assetLine string) []logoCell {
	cells := make([]logoCell, 0, len(assetLine)/logoCellWidth)
	for cellStart := 0; cellStart+logoCellWidth <= len(assetLine); cellStart += logoCellWidth {
		cells = append(cells, logoCell{
			hexColor: assetLine[cellStart : cellStart+logoColorWidth],
			glyph:    assetLine[cellStart+logoColorWidth : cellStart+logoCellWidth],
		})
	}
	return cells
}

func renderLogoCells(cells []logoCell, styleByColor map[string]lipgloss.Style) string {
	segments := make([]string, 0, len(cells))
	runStart := 0
	for cellIndex := 1; cellIndex <= len(cells); cellIndex++ {
		if cellIndex < len(cells) && cells[cellIndex].hexColor == cells[runStart].hexColor {
			continue
		}
		segments = append(segments, renderLogoRun(cells[runStart:cellIndex], styleByColor))
		runStart = cellIndex
	}
	return strings.Join(segments, "")
}

func renderLogoRun(run []logoCell, styleByColor map[string]lipgloss.Style) string {
	glyphs := make([]string, 0, len(run))
	for _, cell := range run {
		glyphs = append(glyphs, cell.glyph)
	}
	if run[0].hexColor == logoBackgroundColor {
		return strings.Repeat(" ", len(run))
	}
	return logoStyleFor(run[0].hexColor, styleByColor).Render(strings.Join(glyphs, ""))
}

func logoStyleFor(hexColor string, styleByColor map[string]lipgloss.Style) lipgloss.Style {
	if style, isKnown := styleByColor[hexColor]; isKnown {
		return style
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#" + hexColor))
	styleByColor[hexColor] = style
	return style
}

func logoWidth() int {
	lines := logoLines()
	if len(lines) == 0 {
		return 0
	}
	return lipgloss.Width(lines[0])
}

func logoHeight() int {
	return len(logoLines())
}

func canRenderLogo(width int, height int) bool {
	return width >= logoWidth() && height >= logoHeight()+minimumHeightBesidesLogo
}

func renderLogo(width int, height int) string {
	if !canRenderLogo(width, height) {
		return ""
	}
	return strings.Join(logoLines(), "\n")
}

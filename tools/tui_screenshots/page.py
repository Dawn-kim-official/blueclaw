from __future__ import annotations

import html

from .terminal_capture import Cell

TERMINAL_BACKGROUND = "#16161e"
FONT_FAMILY = "'HesalcheMono', 'SF Mono', 'DejaVu Sans Mono', Menlo, monospace"
FONT_SIZE_PIXELS = 22
LINE_HEIGHT_RATIO = 1.15
CELL_WIDTH_RATIO = 0.5
PADDING_PIXELS = 24

PAGE_TEMPLATE = """<!DOCTYPE html><html><head><meta charset="utf-8"><style>
html,body{{margin:0;height:100%;background:{background}}}
body{{display:flex;align-items:center;justify-content:center}}
pre{{margin:0;padding:{padding}px;font-family:{fontFamily};
font-size:{fontSize}px;line-height:{lineHeight};white-space:pre}}
</style></head><body><pre>{content}</pre></body></html>"""


def render_cell(cell: Cell) -> str:
    style = "color:" + cell.foreground
    if cell.background:
        style += ";background:" + cell.background
    if cell.is_bold:
        style += ";font-weight:700"
    return f'<span style="{style}">{html.escape(cell.glyph)}</span>'


def render_page(grid: list[list[Cell]]) -> str:
    content = "\n".join("".join(render_cell(cell) for cell in row) for row in grid)
    return PAGE_TEMPLATE.format(
        background=TERMINAL_BACKGROUND,
        fontFamily=FONT_FAMILY,
        padding=PADDING_PIXELS,
        fontSize=FONT_SIZE_PIXELS,
        lineHeight=LINE_HEIGHT_RATIO,
        content=content,
    )


def page_size(columns: int, rows: int) -> tuple[int, int]:
    width = round(columns * FONT_SIZE_PIXELS * CELL_WIDTH_RATIO) + 2 * PADDING_PIXELS
    height = round(rows * FONT_SIZE_PIXELS * LINE_HEIGHT_RATIO) + 2 * PADDING_PIXELS
    return width, height

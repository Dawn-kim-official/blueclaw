from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path

from .chrome import shoot_page
from .fixture_api import serve_fixture_api
from .page import page_size, render_page
from .terminal_capture import capture_screen

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT_DIRECTORY = REPOSITORY_ROOT / "assets" / "screenshots"
DEFAULT_FIXTURE_PORT = 8099

SCREEN_KEY_SECONDS = 0.5


class Frame:
    def __init__(self, name: str, screen_key: str, columns: int, rows: int, settle_seconds: float):
        self.name = name
        self.screen_key = screen_key
        self.columns = columns
        self.rows = rows
        self.settle_seconds = settle_seconds

    def key_schedule(self) -> list[tuple[float, str]]:
        return [(SCREEN_KEY_SECONDS, self.screen_key)]


FRAMES = [
    Frame("tui-tasks", screen_key="1", columns=100, rows=34, settle_seconds=1.6),
    Frame("tui-detail", screen_key="2", columns=100, rows=34, settle_seconds=1.6),
    Frame("tui-approvals", screen_key="3", columns=100, rows=34, settle_seconds=1.6),
    Frame("tui-harness", screen_key="4", columns=100, rows=34, settle_seconds=1.6),
]


def without_trailing_blank_rows(grid):
    last_filled_index = -1
    for row_index, row in enumerate(grid):
        if any(cell.glyph.strip() or cell.background for cell in row):
            last_filled_index = row_index
    return grid[:last_filled_index + 1]


def build_command_line_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Shoot the terminal user interface screenshots the README embeds, from the current working tree."
    )
    parser.add_argument("--only", action="append", default=[], metavar="FRAME",
                        help="shoot only these frames; repeatable. " + ", ".join(frame.name for frame in FRAMES))
    parser.add_argument("--output-directory", type=Path, default=DEFAULT_OUTPUT_DIRECTORY)
    parser.add_argument("--port", type=int, default=DEFAULT_FIXTURE_PORT,
                        help="port the seeded admin API listens on; it shows up in the header bar")
    parser.add_argument("--keep-pages", action="store_true", help="keep the intermediate HTML next to the images")
    return parser


def selected_frames(requested_names: list[str]) -> list[Frame]:
    if not requested_names:
        return FRAMES
    known_names = {frame.name for frame in FRAMES}
    unknown = [name for name in requested_names if name not in known_names]
    if unknown:
        raise SystemExit(f"unknown frame(s): {', '.join(unknown)}; known: {', '.join(sorted(known_names))}")
    return [frame for frame in FRAMES if frame.name in requested_names]


REVISION_SYMBOL = "github.com/yeomyeonggeori/blueclaw/internal/tui.injectedRevision"


def repository_revision() -> str:
    return subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=REPOSITORY_ROOT, check=True, capture_output=True, text=True,
    ).stdout.strip()


def build_command_line_binary(build_directory: Path) -> Path:
    binary_path = build_directory / "blueclaw-cli"
    subprocess.run(
        ["go", "build", "-ldflags", f"-X {REVISION_SYMBOL}={repository_revision()}",
         "-o", str(binary_path), "./cmd/blueclaw-cli"],
        cwd=REPOSITORY_ROOT,
        check=True,
    )
    return binary_path


def write_enrolled_home(home_directory: Path, port: int) -> Path:
    home_directory.mkdir(parents=True, exist_ok=True)
    (home_directory / "runtime.json").write_text(json.dumps({"baseURL": f"http://127.0.0.1:{port}"}))
    (home_directory / "policy.json").write_text("{}")
    return home_directory


def shoot_frame(frame: Frame, binary_path: Path, home_directory: Path, output_directory: Path, keep_pages: bool) -> Path:
    grid = capture_screen(
        command=[str(binary_path)],
        environment={
            "TERM": "xterm-256color",
            "COLORTERM": "truecolor",
            "BLUECLAW_HOME": str(home_directory),
        },
        columns=frame.columns,
        rows=frame.rows,
        key_schedule=frame.key_schedule(),
        settle_seconds=frame.settle_seconds,
    )
    grid = without_trailing_blank_rows(grid)
    page_path = output_directory / (frame.name + ".html")
    page_path.write_text(render_page(grid))
    image_path = output_directory / (frame.name + ".png")
    width, height = page_size(frame.columns, len(grid))
    shoot_page(page_path, image_path, width, height)
    if not keep_pages:
        page_path.unlink()
    return image_path


def describe_path(path: Path) -> str:
    if path.is_relative_to(REPOSITORY_ROOT):
        return str(path.relative_to(REPOSITORY_ROOT))
    return str(path)


def main() -> None:
    arguments = build_command_line_parser().parse_args()
    frames = selected_frames(arguments.only)
    arguments.output_directory.mkdir(parents=True, exist_ok=True)

    fixture_server = serve_fixture_api(arguments.port)
    try:
        with tempfile.TemporaryDirectory() as workspace:
            binary_path = build_command_line_binary(Path(workspace))
            home_directory = write_enrolled_home(Path(workspace) / "home", arguments.port)
            for frame in frames:
                image_path = shoot_frame(
                    frame, binary_path, home_directory, arguments.output_directory, arguments.keep_pages
                )
                print("shot " + describe_path(image_path), file=sys.stderr)
    finally:
        fixture_server.shutdown()

from __future__ import annotations

import fcntl
import os
import pty
import select
import signal
import struct
import termios
import time

import pyte

ANSI_NAMED_COLORS = {
    "black": "000000",
    "red": "cd0000",
    "green": "00cd00",
    "brown": "cdcd00",
    "blue": "0000ee",
    "magenta": "cd00cd",
    "cyan": "00cdcd",
    "white": "e5e5e5",
}


class Cell:
    def __init__(self, glyph: str, foreground: str, background: str, is_bold: bool):
        self.glyph = glyph
        self.foreground = foreground
        self.background = background
        self.is_bold = is_bold


def resolve_color(value: str, fallback: str) -> str:
    if value in ("default", None):
        return fallback
    return "#" + ANSI_NAMED_COLORS.get(value, value)


def start_in_pseudo_terminal(command: list[str], environment: dict[str, str], columns: int, rows: int):
    process_id, terminal_fd = pty.fork()
    if process_id == 0:
        os.environ.update(environment)
        os.execvp(command[0], command)
    fcntl.ioctl(terminal_fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
    return process_id, terminal_fd


FIRST_OUTPUT_TIMEOUT_SECONDS = 30.0


def wait_for_first_output(terminal_fd: int, stream: pyte.ByteStream) -> None:
    if not select.select([terminal_fd], [], [], FIRST_OUTPUT_TIMEOUT_SECONDS)[0]:
        raise RuntimeError(
            f"the client drew nothing within {FIRST_OUTPUT_TIMEOUT_SECONDS:.0f}s of starting; "
            "it either failed to launch or is waiting on something"
        )
    stream.feed(os.read(terminal_fd, 65536))


def capture_screen(
    command: list[str],
    environment: dict[str, str],
    columns: int,
    rows: int,
    key_schedule: list[tuple[float, str]],
    settle_seconds: float,
) -> list[list[Cell]]:
    process_id, terminal_fd = start_in_pseudo_terminal(command, environment, columns, rows)
    screen = pyte.Screen(columns, rows)
    stream = pyte.ByteStream(screen)

    pending_keys = sorted(key_schedule)
    try:
        wait_for_first_output(terminal_fd, stream)
        started_at = time.time()
        while True:
            elapsed = time.time() - started_at
            if elapsed >= settle_seconds:
                break
            while pending_keys and pending_keys[0][0] <= elapsed:
                os.write(terminal_fd, pending_keys.pop(0)[1].encode())
            next_deadline = min(settle_seconds, pending_keys[0][0]) if pending_keys else settle_seconds
            timeout = max(next_deadline - elapsed, 0.01)
            if not select.select([terminal_fd], [], [], timeout)[0]:
                continue
            try:
                chunk = os.read(terminal_fd, 65536)
            except OSError:
                break
            if not chunk:
                break
            stream.feed(chunk)
    finally:
        os.kill(process_id, signal.SIGKILL)
        os.waitpid(process_id, 0)
        os.close(terminal_fd)

    return read_cells(screen, columns, rows)


def read_cells(screen: pyte.Screen, columns: int, rows: int) -> list[list[Cell]]:
    grid = []
    for row_index in range(rows):
        line = screen.buffer[row_index]
        grid.append([
            Cell(
                glyph=line[column_index].data or " ",
                foreground=resolve_color(line[column_index].fg, "#d7dde3"),
                background=resolve_color(line[column_index].bg, ""),
                is_bold=line[column_index].bold,
            )
            for column_index in range(columns)
        ])
    return grid

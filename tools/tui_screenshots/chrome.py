from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

CHROME_CANDIDATES = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "google-chrome",
    "chromium",
    "chromium-browser",
]

DEVICE_SCALE_FACTOR = 2


def find_chrome() -> str:
    for candidate in CHROME_CANDIDATES:
        if Path(candidate).exists():
            return candidate
        resolved = shutil.which(candidate)
        if resolved:
            return resolved
    raise RuntimeError("no Chrome or Chromium found; looked for " + ", ".join(CHROME_CANDIDATES))


def shoot_page(page_path: Path, output_path: Path, width: int, height: int) -> None:
    subprocess.run(
        [
            find_chrome(),
            "--headless=new",
            "--disable-gpu",
            "--hide-scrollbars",
            f"--force-device-scale-factor={DEVICE_SCALE_FACTOR}",
            f"--window-size={width},{height}",
            f"--screenshot={output_path}",
            page_path.as_uri(),
        ],
        check=True,
        capture_output=True,
    )

"""Render the demo app headless to SVG/PNG for visual inspection."""

import asyncio
import subprocess
import sys
from pathlib import Path

from llmtop.app import LlmTopApp
from llmtop.config import default_config
from llmtop.demo import DemoMonitor
from llmtop.palette import RGB, TerminalColors

OUTPUT_SVG = Path("/tmp/opencode/llmtop-demo.svg")
OUTPUT_PNG = Path("/tmp/opencode/llmtop-demo.png")


async def run() -> None:
    config = default_config()
    colors = TerminalColors(background=RGB(22, 24, 30), foreground=RGB(220, 220, 220))
    app = LlmTopApp(config, monitor=DemoMonitor(config), terminal_colors=colors)
    async with app.run_test(size=(150, 46)) as pilot:
        await pilot.pause()
        await app.refresh_now()
        await pilot.pause()
        OUTPUT_SVG.write_text(app.export_screenshot(title="llmtop"))


asyncio.run(run())
result = subprocess.run(
    [ "magick", str(OUTPUT_SVG), str(OUTPUT_PNG) ],
    capture_output=True,
    text=True,
)
if result.returncode != 0:
    print(result.stderr.strip()[:400])
    sys.exit(1)
print("wrote", OUTPUT_PNG)

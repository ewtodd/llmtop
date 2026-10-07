"""Render the demo app headless and print the screenshot as a text grid."""

import asyncio
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

from llmtop.app import LlmTopApp
from llmtop.config import default_config
from llmtop.demo import DemoMonitor

OUTPUT = Path("/tmp/opencode/llmtop-demo.svg")


async def run() -> None:
    config = default_config()
    app = LlmTopApp(config, monitor=DemoMonitor(config))
    async with app.run_test(size=(150, 46)) as pilot:
        await pilot.pause()
        await app.refresh_now()
        await pilot.pause()
        OUTPUT.write_text(app.export_screenshot(title="llmtop"))


def render_grid(svg_path: Path) -> list[str]:
    root = ET.fromstring(svg_path.read_text())
    rows: dict[float, list[tuple[float, str]]] = {}
    for node in root.iter():
        if not node.tag.endswith("text"):
            continue
        x = float(node.attrib.get("x", "0"))
        y = float(node.attrib.get("y", "0"))
        content = "".join(node.itertext())
        rows.setdefault(y, []).append((x, content))
    lines = []
    for y in sorted(rows):
        cells = sorted(rows[y])
        step = 8.4
        width = int(cells[-1][0] / step) + len(cells[-1][1]) + 2
        buffer = [" "] * width
        for x, content in cells:
            start = int(round(x / step))
            for offset, char in enumerate(content):
                position = start + offset
                if position < width:
                    buffer[position] = char
        lines.append("".join(buffer).rstrip())
    return lines


asyncio.run(run())
for line in render_grid(OUTPUT):
    print(line)
sys.exit(0)

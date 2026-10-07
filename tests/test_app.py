import asyncio
import xml.etree.ElementTree as ET

from llmtop.app import EngineCard, LlmTopApp, OriginTable, StatusBar
from llmtop.config import default_config
from llmtop.demo import DemoMonitor


def _screenshot_text(svg: str) -> str:
    root = ET.fromstring(svg)
    text = "".join(
        "".join(node.itertext())
        for node in root.iter()
        if node.tag.endswith("text")
    )
    return text.replace("\xa0", " ")


def test_app_renders_demo_snapshot():
    async def run() -> None:
        config = default_config()
        app = LlmTopApp(config, monitor=DemoMonitor(config))
        async with app.run_test(size=(150, 46)) as pilot:
            await pilot.pause()
            await app.refresh_now()
            await pilot.pause()
            assert len(app.query(EngineCard)) == len(config.engines)
            assert app.query_one(OriginTable).row_count == 4
            status = app.query_one(StatusBar)
            assert "gateway" in str(status.render())
            await pilot.press("o")
            await pilot.press("p")
            assert app._paused is True
            await pilot.press("p")
            assert app._paused is False

    asyncio.run(run())


def test_app_handles_no_engines():
    async def run() -> None:
        config = default_config()
        config.engines = []
        app = LlmTopApp(config, monitor=DemoMonitor(config))
        async with app.run_test(size=(120, 40)) as pilot:
            await pilot.pause()
            await app.refresh_now()
            await pilot.pause()
            assert len(app.query(EngineCard)) == 0
            assert app.query("#engines-empty")

    asyncio.run(run())


def test_app_short_terminal_keeps_cards_intact():
    # Regression: short terminals used to overflow, scroll, and hide the first
    # card's top rows.
    async def run(width: int, height: int) -> None:
        config = default_config()
        app = LlmTopApp(config, monitor=DemoMonitor(config))
        async with app.run_test(size=(width, height)) as pilot:
            await pilot.pause()
            await app.refresh_now()
            await pilot.pause()
            text = _screenshot_text(app.export_screenshot())
            assert "qwen3.8-27b (vLLM)" in text
            assert "qwen3.8-flash-next (gufo)" in text
            assert text.count("DECODE") == len(config.engines)
            assert text.count("prefill avg") == len(config.engines)

    asyncio.run(run(100, 30))
    asyncio.run(run(80, 24))


def test_app_pins_theme_when_configured():
    async def run() -> None:
        config = default_config()
        config.theme = "textual-dark"
        app = LlmTopApp(config, monitor=DemoMonitor(config))
        async with app.run_test(size=(120, 40)) as pilot:
            await pilot.pause()
            assert app.theme == "textual-dark"

    asyncio.run(run())

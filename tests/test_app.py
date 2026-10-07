import asyncio

from llmtop.app import EngineTable, LlmTopApp, OriginTable, StatusBar
from llmtop.config import default_config
from llmtop.demo import DemoMonitor


def test_app_renders_demo_snapshot():
    async def run() -> None:
        config = default_config()
        app = LlmTopApp(config, monitor=DemoMonitor(config))
        async with app.run_test(size=(140, 44)) as pilot:
            await pilot.pause()
            await app.refresh_now()
            assert app.query_one(EngineTable).row_count == len(config.engines)
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
            assert app.query_one(EngineTable).row_count == 1

    asyncio.run(run())

"""The Textual TUI."""

from __future__ import annotations

import asyncio
import socket
import time

from rich.text import Text
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.widgets import DataTable, Footer, Header, RichLog, Static

from .config import Config
from .monitor import LiveMonitor, Monitor
from .snapshot import EngineState, OriginRow, RequestEvent, Snapshot

SPARK_BLOCKS = "▁▂▃▄▅▆▇█"
ENGINE_COLUMNS = (
    "Engine",
    "Model",
    "St",
    "Run",
    "Wait",
    "Prefill",
    "Decode",
    "Pref avg",
    "Dec avg",
    "TTFT",
    "KV%",
    "Accept",
    "Decode history",
)
ORIGIN_COLUMNS = (
    "Key",
    "App",
    "Machine",
    "Req",
    "Live",
    "Fail",
    "Prefill tok/s",
    "Decode tok/s",
    "Avg",
    "Last",
    "Models",
)
SORT_MODES = ("requests", "tokens", "latency", "last")


class StatusBar(Static):
    pass


class EngineTable(DataTable):
    def on_mount(self) -> None:
        self.cursor_type = "row"
        self.zebra_stripes = True
        self.add_columns(*ENGINE_COLUMNS)
        self.fixed_columns = 1


class OriginTable(DataTable):
    def on_mount(self) -> None:
        self.cursor_type = "row"
        self.zebra_stripes = True
        self.add_columns(*ORIGIN_COLUMNS)


class FeedLog(RichLog):
    def on_mount(self) -> None:
        self.max_lines = 500
        self.wrap = False
        self.highlight = False
        self.markup = False


class LlmTopApp(App):
    CSS = """
    Screen {
        layout: vertical;
    }
    #engines {
        height: 8;
    }
    #origins {
        height: 1fr;
    }
    #feed {
        height: 12;
        border-top: solid $panel;
    }
    #status {
        height: 1;
        background: $panel;
        color: $text-muted;
        padding: 0 1;
    }
    """
    BINDINGS = [
        Binding("q", "quit", "Quit"),
        Binding("p", "toggle_pause", "Pause"),
        Binding("r", "refresh", "Refresh"),
        Binding("o", "cycle_sort", "Sort"),
        Binding("c", "clear_feed", "Clear feed"),
    ]

    def __init__(self, config: Config, monitor: Monitor | None = None) -> None:
        super().__init__()
        self.config = config
        self.monitor = monitor if monitor is not None else LiveMonitor(config)
        self.snapshot = Snapshot()
        self._paused = False
        self._refreshing = False
        self._force_refresh = False
        self._sort_mode = 0
        self._seen_ids: set[str] = set()
        self._feed_order: list[str] = []

    def compose(self) -> ComposeResult:
        yield Header(show_clock=True)
        yield EngineTable(id="engines")
        yield OriginTable(id="origins")
        yield FeedLog(id="feed")
        yield StatusBar(id="status")
        yield Footer()

    async def on_mount(self) -> None:
        self.title = "llmtop"
        self.sub_title = f"{socket.gethostname()} -> {self.config.gateway.url}"
        self.set_interval(self.config.poll_interval, self.refresh_data)
        self.refresh_data()

    def refresh_data(self) -> None:
        if self._paused or self._refreshing:
            return
        self._refreshing = True
        self.run_worker(self._poll(), name="poll")

    async def _poll(self) -> None:
        try:
            snapshot = await asyncio.to_thread(self.monitor.snapshot, self._force_refresh)
            self._force_refresh = False
            self.snapshot = snapshot
            self.render_snapshot(snapshot)
        finally:
            self._refreshing = False

    async def refresh_now(self) -> None:
        self._force_refresh = True
        snapshot = await asyncio.to_thread(self.monitor.snapshot, True)
        self._force_refresh = False
        self.snapshot = snapshot
        self.render_snapshot(snapshot)

    def render_snapshot(self, snapshot: Snapshot) -> None:
        self._render_engines(snapshot.engines)
        self._render_origins(snapshot.origins)
        self._render_feed(snapshot.feed)
        self._render_status(snapshot)

    def _render_engines(self, engines: list[EngineState]) -> None:
        table = self.query_one(EngineTable)
        table.clear()
        for engine in engines:
            table.add_row(
                Text(engine.name, style="bold"),
                Text(",".join(engine.models) or "-", style="dim"),
                Text("up", style="green") if engine.ok else Text("down", style="bold red"),
                str(engine.running),
                _wait_text(engine.waiting),
                _rate_text(engine.prefill_now, "cyan"),
                _rate_text(engine.decode_now, "bold cyan"),
                _rate_text(engine.prefill_avg, "dim cyan"),
                _rate_text(engine.decode_avg, "dim cyan"),
                _latency_text(engine.ttft_ms),
                _kv_text(engine.kv_usage),
                _accept_text(engine.spec_accept),
                Text(_sparkline(engine.decode_history, 24), style="cyan"),
            )
        if not engines:
            table.add_row(Text("no engines configured", style="dim"), *[""] * (len(ENGINE_COLUMNS) - 1))

    def _render_origins(self, origins: list[OriginRow]) -> None:
        table = self.query_one(OriginTable)
        table.clear()
        for row in self._sorted_origins(origins):
            table.add_row(
                Text(row.virtual_key, style="bold"),
                Text(row.app, style=""),
                _machine_text(row),
                str(row.requests),
                Text(str(row.inflight), style="yellow") if row.inflight else Text("0", style="dim"),
                Text(str(row.failed), style="red") if row.failed else Text("0", style="dim"),
                _rate_text(row.prompt_tps, "cyan"),
                _rate_text(row.completion_tps, "bold cyan"),
                _latency_text(row.avg_latency_ms),
                Text(_fmt_age(time.time() - row.last_seen), style="dim"),
                Text(",".join(row.models) or "-", style="dim"),
            )
        if not origins:
            message = "no recent requests"
            if not self.snapshot.gateway_ok:
                message = "origins unavailable: " + (self.snapshot.gateway_error or "gateway not reachable")
            table.add_row(Text(message, style="dim"), *[""] * (len(ORIGIN_COLUMNS) - 1))

    def _sorted_origins(self, origins: list[OriginRow]) -> list[OriginRow]:
        mode = SORT_MODES[self._sort_mode]
        if mode == "requests":
            return sorted(origins, key=lambda row: (row.requests, row.completion_tokens), reverse=True)
        if mode == "tokens":
            return sorted(origins, key=lambda row: row.prompt_tokens + row.completion_tokens, reverse=True)
        if mode == "latency":
            return sorted(origins, key=lambda row: row.avg_latency_ms or 0.0, reverse=True)
        return sorted(origins, key=lambda row: row.last_seen, reverse=True)

    def _render_feed(self, events: list[RequestEvent]) -> None:
        feed = self.query_one(FeedLog)
        new_events = [event for event in events if event.request_id not in self._seen_ids]
        if not new_events:
            return
        for event in sorted(new_events, key=lambda item: item.timestamp):
            feed.write(_feed_line(event))
            self._seen_ids.add(event.request_id)
            self._feed_order.append(event.request_id)
        while len(self._feed_order) > 4000:
            stale = self._feed_order.pop(0)
            self._seen_ids.discard(stale)

    def _render_status(self, snapshot: Snapshot) -> None:
        status = self.query_one(StatusBar)
        engines_up = sum(1 for engine in snapshot.engines if engine.ok)
        text = Text()
        if self._paused:
            text.append("PAUSED  ", style="bold yellow")
        text.append(f"gateway {'ok' if snapshot.gateway_ok else 'down'}", style="green" if snapshot.gateway_ok else "red")
        text.append(f"  engines {engines_up}/{len(snapshot.engines)}")
        text.append(f"  req/5m {snapshot.total_requests} ({snapshot.requests_per_minute:.1f}/min)")
        text.append(f"  prompt {_fmt_tokens(snapshot.total_prompt_tokens)}")
        text.append(f"  decode {_fmt_tokens(snapshot.total_completion_tokens)}")
        text.append(f"  inflight {snapshot.inflight}", style="yellow" if snapshot.inflight else "")
        text.append(f"  journal {snapshot.journal_status}")
        if snapshot.gateway_error:
            text.append(f"  {snapshot.gateway_error}", style="red")
        status.update(text)

    def action_toggle_pause(self) -> None:
        self._paused = not self._paused
        self._render_status(self.snapshot)

    def action_refresh(self) -> None:
        self.run_worker(self.refresh_now(), name="refresh")

    def action_cycle_sort(self) -> None:
        self._sort_mode = (self._sort_mode + 1) % len(SORT_MODES)
        self._render_origins(self.snapshot.origins)

    def action_clear_feed(self) -> None:
        self.query_one(FeedLog).clear()
        self._seen_ids.clear()
        self._feed_order.clear()

    def on_unmount(self) -> None:
        self.monitor.close()


def _sparkline(values: list[float], width: int) -> str:
    if not values:
        return ""
    data = values[-width:]
    top = max(data)
    if top <= 0:
        return SPARK_BLOCKS[0] * len(data)
    chars = [
        SPARK_BLOCKS[min(len(SPARK_BLOCKS) - 1, max(0, round(value / top * (len(SPARK_BLOCKS) - 1))))]
        for value in data
    ]
    return "".join(chars)


def _rate_text(value: float, style: str) -> Text:
    return Text(_fmt_rate(value), style=style)


def _wait_text(waiting: int) -> Text:
    if waiting <= 0:
        return Text("0", style="dim")
    return Text(str(waiting), style="bold yellow")


def _latency_text(value: float | None) -> Text:
    if value is None:
        return Text("-", style="dim")
    return Text(_fmt_ms(value))


def _kv_text(value: float | None) -> Text:
    if value is None:
        return Text("-", style="dim")
    style = "green"
    if value >= 0.85:
        style = "bold red"
    elif value >= 0.6:
        style = "yellow"
    return Text(f"{value * 100:.0f}%", style=style)


def _accept_text(value: float | None) -> Text:
    if value is None:
        return Text("-", style="dim")
    return Text(f"{value * 100:.0f}%")


def _machine_text(row: OriginRow) -> Text:
    text = Text()
    if row.host:
        text.append(row.host)
        text.append(f" {row.address}", style="dim")
    else:
        text.append(row.address or "unknown", style="dim")
    return text


def _feed_line(event: RequestEvent) -> Text:
    timestamp = time.strftime("%H:%M:%S", time.localtime(event.timestamp))
    line = Text()
    line.append(timestamp, style="dim")
    line.append(" ")
    line.append(f"{event.provider:<5}", style="magenta")
    line.append(f" {event.virtual_key:<11}", style="bold")
    machine = event.host or event.address or "?"
    line.append(f"{machine:<14}", style="")
    line.append(f" +{event.prompt_tokens:<6}", style="cyan")
    line.append(f" -{event.completion_tokens:<5}", style="bold cyan")
    if event.latency_ms is not None:
        line.append(f" {_fmt_ms(event.latency_ms):>7}", style="")
    line.append(f" {event.model}", style="dim")
    if event.inflight:
        line.append("  running", style="yellow")
    elif event.failed:
        line.append(f"  {event.status}", style="bold red")
    else:
        line.append("  ok", style="green")
    return line


def _fmt_rate(value: float) -> str:
    if value >= 10000:
        return f"{value / 1000:.0f}k"
    if value >= 1000:
        return f"{value / 1000:.1f}k"
    return f"{value:.0f}"


def _fmt_ms(value: float) -> str:
    if value >= 1000:
        return f"{value / 1000:.1f}s"
    return f"{value:.0f}ms"


def _fmt_tokens(value: int) -> str:
    if value >= 1_000_000:
        return f"{value / 1_000_000:.1f}M"
    if value >= 1000:
        return f"{value / 1000:.1f}k"
    return str(value)


def _fmt_age(seconds: float) -> str:
    if seconds < 1:
        return "now"
    if seconds < 60:
        return f"{seconds:.0f}s"
    if seconds < 3600:
        return f"{seconds / 60:.0f}m"
    return f"{seconds / 3600:.1f}h"

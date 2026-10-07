"""The Textual TUI."""

from __future__ import annotations

import asyncio
import socket
import time

from rich.text import Text
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import VerticalGroup
from textual.widgets import DataTable, Footer, Header, RichLog, Static

from .config import Config
from .monitor import LiveMonitor, Monitor
from .palette import TerminalColors, build_palette, theme_name
from .render import (
    engine_card_renderable,
    format_age,
    format_ms,
    format_rate,
    format_tokens,
)
from .snapshot import EngineState, OriginRow, RequestEvent, Snapshot

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


class EngineCard(Static):
    def __init__(self, state: EngineState, headline_interval: float = 3.0) -> None:
        super().__init__(id=f"engine-{state.id}")
        self.engine_id = state.id
        self._state = state
        self._headline_interval = headline_interval
        self._redraw()

    def update_state(self, state: EngineState) -> None:
        self._state = state
        self._redraw()

    def on_mount(self) -> None:
        self._redraw()

    def on_resize(self) -> None:
        self._redraw()

    def _redraw(self) -> None:
        state = self._state
        width = self.content_size.width if self.is_mounted else 0
        if width <= 0:
            width = 100
        models = ", ".join(state.models) or state.url
        self.border_title = state.name
        self.border_subtitle = f"{models}  ·  run {state.running} wait {state.waiting}"
        self.set_class(state.ok, "ok")
        self.set_class(not state.ok, "down")
        self.update(engine_card_renderable(state, width, self._headline_interval))


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
        height: auto;
    }
    EngineCard {
        height: 8;
        border: round $border;
        background: $surface;
        padding: 0 1;
        margin-bottom: 1;
    }
    EngineCard.down {
        border: round $error;
    }
    #origins {
        height: 1fr;
        min-height: 0;
    }
    /* The ANSI themes force a bright-blue table header; tone it to a generated
       surface so the table belongs to the terminal's own palette. */
    OriginTable:ansi > .datatable--header {
        background: $panel-lighten-1;
        color: $text;
    }
    #feed {
        height: 1fr;
        max-height: 10;
        min-height: 0;
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

    def __init__(
        self,
        config: Config,
        monitor: Monitor | None = None,
        terminal_colors: TerminalColors | None = None,
    ) -> None:
        self.config = config
        self.monitor = monitor if monitor is not None else LiveMonitor(config)
        self.snapshot = Snapshot()
        self.terminal_colors = terminal_colors
        self._surfaces = build_palette(terminal_colors) if terminal_colors is not None else {}
        self._theme = config.theme if config.theme != "auto" else theme_name(terminal_colors)
        self._paused = False
        self._refreshing = False
        self._force_refresh = False
        self._sort_mode = 0
        self._seen_ids: set[str] = set()
        self._feed_order: list[str] = []
        super().__init__()
        self.theme = self._theme

    def get_css_variables(self) -> dict[str, str]:
        variables = dict(super().get_css_variables())
        variables.update(
            {
                "primary": "ansi_yellow",
                "secondary": "ansi_cyan",
                "success": "ansi_green",
                "warning": "ansi_yellow",
                "error": "ansi_red",
                "accent": "ansi_yellow",
            }
        )
        variables.update(self._surfaces)
        return variables

    def compose(self) -> ComposeResult:
        yield Header(show_clock=True)
        yield VerticalGroup(id="engines")
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
        panel = self.query_one("#engines", VerticalGroup)
        if not engines:
            if not list(panel.query("#engines-empty")):
                message = "no engines configured — add [[engines]] to llmtop.toml"
                panel.mount(Static(message, id="engines-empty"))
            return
        for placeholder in list(panel.query("#engines-empty")):
            placeholder.remove()
        cards = {card.engine_id: card for card in panel.query(EngineCard)}
        for state in engines:
            card = cards.pop(state.id, None)
            if card is None:
                panel.mount(EngineCard(state, self.config.headline_interval_seconds))
            else:
                card.update_state(state)
        for stale in cards.values():
            stale.remove()

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
                Text(format_ms(row.avg_latency_ms), style="" if row.avg_latency_ms else "dim"),
                Text(format_age(time.time() - row.last_seen), style="dim"),
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
        text.append(
            f"gateway {'ok' if snapshot.gateway_ok else 'down'}",
            style="green" if snapshot.gateway_ok else "red",
        )
        text.append(f"  engines {engines_up}/{len(snapshot.engines)}")
        text.append(f"  req/5m {snapshot.total_requests} ({snapshot.requests_per_minute:.1f}/min)")
        text.append(f"  prompt {format_tokens(snapshot.total_prompt_tokens)}")
        text.append(f"  decode {format_tokens(snapshot.total_completion_tokens)}")
        text.append(f"  inflight {snapshot.inflight}", style="yellow" if snapshot.inflight else "")
        text.append(f"  journal {snapshot.journal_status}")
        if snapshot.gateway_error:
            text.append(f"  {snapshot.gateway_error}", style="red")
        status.update(text)

    def action_toggle_pause(self) -> None:
        self._paused = not self._paused
        self._render_status(self.snapshot)

    def action_refresh(self) -> None:
        self._force_refresh = True
        if self._refreshing:
            return
        self._refreshing = True
        self.run_worker(self._poll(), name="poll")

    def action_cycle_sort(self) -> None:
        self._sort_mode = (self._sort_mode + 1) % len(SORT_MODES)
        self._render_origins(self.snapshot.origins)

    def action_clear_feed(self) -> None:
        self.query_one(FeedLog).clear()
        self._seen_ids.clear()
        self._feed_order.clear()

    def on_unmount(self) -> None:
        self.monitor.close()


def _rate_text(value: float, style: str) -> Text:
    return Text(format_rate(value), style=style)


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
        line.append(f" {format_ms(event.latency_ms):>7}", style="")
    line.append(f" {event.model}", style="dim")
    if event.inflight:
        line.append("  running", style="yellow")
    elif event.failed:
        line.append(f"  {event.status}", style="bold red")
    else:
        line.append("  ok", style="green")
    return line

"""Rendering primitives for the engine cards.

Everything returns Rich renderables or lists of lines (one Text per row) so
the same primitives can be unit tested without a terminal.  The card is
composed as fixed-width lines instead of a Rich table: a table that does not
fit the widget width wraps its cells and then overflows the card's fixed
height, which is exactly how the old layout corrupted itself on narrow
terminals.
"""

from __future__ import annotations

import math

from rich.console import Group
from rich.text import Text

from .snapshot import EngineState

BIG_ROWS = 5
CARD_ROWS = BIG_ROWS + 1
BIG_CHAR_WIDTH = 4  # three glyph columns plus one separator

BIG_GLYPHS: dict[str, tuple[str, str, str, str, str]] = {
    "0": ("███", "█ █", "█ █", "█ █", "███"),
    "1": (" █ ", "██ ", " █ ", " █ ", "███"),
    "2": ("███", "  █", "███", "█  ", "███"),
    "3": ("███", "  █", "███", "  █", "███"),
    "4": ("█ █", "█ █", "███", "  █", "  █"),
    "5": ("███", "█  ", "███", "  █", "███"),
    "6": ("███", "█  ", "███", "█ █", "███"),
    "7": ("███", "  █", "  █", "  █", "  █"),
    "8": ("███", "█ █", "███", "█ █", "███"),
    "9": ("███", "█ █", "███", "  █", "███"),
    ".": ("  ", "  ", "  ", "  ", "█ "),
    "-": ("   ", "   ", "███", "   ", "   "),
    " ": ("   ", "   ", "   ", "   ", "   "),
}


def finite(value: object, fallback: float = 0.0) -> float:
    if value is None:
        return fallback
    try:
        number = float(value)
    except (TypeError, ValueError):
        return fallback
    return number if math.isfinite(number) else fallback


def headline_number(value: float) -> str:
    """Format a tok/s headline; capped so the digits can never blow the card up."""
    number = min(finite(value), 9999.0)
    if number >= 100:
        return f"{number:.0f}"
    return f"{number:.1f}"


def big_number_lines(value: str) -> list[Text]:
    rows = [ "" for _ in range(BIG_ROWS) ]
    for character in value:
        glyph = BIG_GLYPHS.get(character)
        if glyph is None:
            continue
        for index in range(BIG_ROWS):
            rows[index] += glyph[index] + " "
    return [ Text(row.rstrip(), style="bold") for row in rows ]


def meter_line(
    label: str,
    value: float,
    maximum: float,
    width: int,
    style: str,
    value_text: str | None = None,
) -> Text:
    current = max(finite(value), 0.0)
    ceiling = finite(maximum, 1.0)
    fraction = 0.0 if ceiling <= 0 else min(current / ceiling, 1.0)
    filled = round(fraction * width)
    text = Text()
    text.append(f"{label:<8}", style="dim")
    text.append("█" * filled, style=style)
    text.append("░" * (width - filled), style="dim")
    shown = value_text if value_text is not None else format_rate(current)
    text.append(f" {shown:>7}", style=style)
    return text


def nice_max(value: float) -> float:
    """Round up to the next 1/2/5 x 10^n so bars don't rescale constantly."""
    number = finite(value)
    if number <= 0:
        return 1.0
    exponent = math.floor(math.log10(number))
    base = 10.0**exponent
    for step in (1.0, 2.0, 5.0, 10.0):
        if number <= step * base:
            return step * base
    return 10.0 * base


def kv_style(usage: float | None) -> str:
    if usage is None:
        return "dim"
    if usage >= 0.85:
        return "bold red"
    if usage >= 0.6:
        return "yellow"
    return "green"


def engine_card_renderable(state: EngineState, width: int = 100, headline_interval: float = 3.0) -> Group:
    width = max(width, 24)
    if not state.ok:
        lines = [
            Text("engine unreachable", style="bold red"),
            Text(state.error or state.url, style="dim"),
            Text("check the URL and that the engine is running", style="dim"),
        ]
        return Group(*[ _truncate(line, width) for line in lines ])

    prefill_big = _big_column("prefill", state.prefill_headline, headline_interval)
    decode_big = _big_column("decode", state.decode_headline, headline_interval)
    # Reserve room for four prefill digits and five decode glyphs so the
    # columns don't shift as values cross 1000, and keep a wide gap between
    # them for the same reason.
    prefill_width = max(max(len(line.plain) for line in prefill_big), 4 * BIG_CHAR_WIDTH)
    decode_width = max(max(len(line.plain) for line in decode_big), 5 * BIG_CHAR_WIDTH)

    meter_width = max(8, min(24, width - prefill_width - decode_width - 12))
    meters = _meter_rows(state, meter_width)

    lines: list[Text] = []
    for index in range(CARD_ROWS):
        line = Text()
        line.append_text(prefill_big[index])
        line.append(" " * max(0, prefill_width - len(prefill_big[index].plain) + 4))
        line.append_text(decode_big[index])
        line.append(" " * max(0, decode_width - len(decode_big[index].plain) + 4))
        line.append_text(meters[index])
        lines.append(_truncate(line, width))
    return Group(*lines)


def _big_column(label: str, value: float, headline_interval: float) -> list[Text]:
    lines = big_number_lines(headline_number(value))
    lines.append(Text(f"{label} {window_label(headline_interval)}", style="dim"))
    return lines


def _truncate(text: Text, width: int) -> Text:
    copy = text.copy()
    copy.truncate(width)
    return copy


def _meter_rows(state: EngineState, width: int) -> list[Text]:
    decode_scale = nice_max(max([ *state.decode_history[-48:], 10.0 ]))
    prefill_scale = nice_max(max(finite(state.prefill_now), finite(state.prefill_headline), 100.0))
    kv_value = finite(state.kv_usage)
    kv_label = f"{kv_value * 100:.0f}%" if state.kv_usage is not None else "n/a"
    rows = [
        meter_line("PREFILL", state.prefill_now, prefill_scale, width, "magenta"),
        meter_line("DECODE", state.decode_now, decode_scale, width, "bold cyan"),
        meter_line("KV", kv_value, 1.0, width, kv_style(state.kv_usage), value_text=kv_label),
        _stats_line(state),
        _detail_line(state),
        Text(""),
    ]
    return rows


def _stats_line(state: EngineState) -> Text:
    text = Text()
    text.append("ttft ", style="dim")
    text.append(format_ms(state.ttft_ms), style="")
    return text


def _detail_line(state: EngineState) -> Text:
    text = Text()
    text.append("spec ", style="dim")
    if state.spec_accept is None or not math.isfinite(finite(state.spec_accept, float("nan"))):
        text.append("n/a", style="dim")
    else:
        text.append(f"{state.spec_accept * 100:.0f}%", style="")
    text.append("   slots ", style="dim")
    if state.total_slots:
        text.append(f"{state.active_slots}/{state.total_slots}", style="")
    else:
        text.append("n/a", style="dim")
    text.append(f"   requests {state.running} run", style="dim")
    if state.waiting:
        text.append(f" {state.waiting} wait", style="bold yellow")
    return text


def window_label(seconds: float) -> str:
    window = max(finite(seconds), 0.0)
    if window < 60:
        return f"{window:.0f}s"
    if window < 3600:
        return f"{window / 60:.0f}m"
    return f"{window / 3600:.1f}h"


def format_rate(value: float) -> str:
    number = finite(value)
    if number >= 10000:
        return f"{number / 1000:.0f}k"
    if number >= 1000:
        return f"{number / 1000:.1f}k"
    if number >= 100:
        return f"{number:.0f}"
    return f"{number:.1f}"


def format_ms(value: float | None) -> str:
    if value is None:
        return "n/a"
    number = finite(value, -1.0)
    if number < 0:
        return "n/a"
    if number >= 1000:
        return f"{number / 1000:.1f}s"
    return f"{number:.0f}ms"


def format_tokens(value: int) -> str:
    number = int(finite(value))
    if number >= 1_000_000:
        return f"{number / 1_000_000:.1f}M"
    if number >= 1000:
        return f"{number / 1000:.1f}k"
    return str(number)


def format_age(seconds: float) -> str:
    number = finite(seconds)
    if number < 1:
        return "now"
    if number < 60:
        return f"{number:.0f}s"
    if number < 3600:
        return f"{number / 60:.0f}m"
    return f"{number / 3600:.1f}h"

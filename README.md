# llmtop

A btop/htop/nvtop-style terminal UI for self-hosted LLM inference. It answers
two questions:

1. **What are my engines doing right now?** Prompt (prefill) and generation
   (decode) tokens/s per engine, plus the per-request average over recent
   requests, running/waiting requests, KV-cache usage, TTFT and speculative
   decode acceptance.
2. **Who is hitting my API?** Requests grouped by Bifrost virtual key and
   client application, with token rates, in-flight requests, failures and
   latency.

Stack matches [son-of-anton](https://github.com/ewtodd/son-of-anton): Python +
[Textual](https://textual.textualize.io/) + Rich, and the default theme is
derived from the terminal the same way (see below).

```
 ╭─ qwen3.8-27b (vLLM) ─────────────────────────────────────────────────────────╮
 │ █ █ █ █ ███     ███ ███    ███    PREFILL ██████████████████████░░   1.2k    │
 │ █ █ █ █ █ █     █   █ █    █      DECODE  ████████████████████████   124     │
 │ ███ ███ ███     ███ █ █    ███    KV      ██████████░░░░░░░░░░░░░░   40%     │
 │   █   █ █ █       █ █ █      █    ttft 180ms   per stream pf 413 dec 41.3   │
 │   █   █ ███     ███ ███ █  ███    spec 61%   slots 2/2   streams 3 +1 wait  │
 │ prefill 1s tot      decode 1s tot                                             │
 ╰─────────────────────── qwen3.8-27b  ·  run 3 wait 1 ─────────────────────────╯
 Key         App           Req Live Fail Prefill Decode ...
 opencode    opencode       36    0    0     105     9.8
 soa         son-of-anton   32    1    0     159    22.4
 open-webui  open-webui     12    0    0      51    14.0
```

## The display

- One card per engine with **two large numbers: aggregate prefill first,
  aggregate decode second**. Aggregates count every concurrent request, use
  tokens actually processed (vLLM prompt-cache hits are not work), and are
  re-sampled over `headline_interval_seconds` and held in between.
- Next to them: the same aggregate metric as live PREFILL / DECODE bars, the
  KV-cache bar, and explicitly `per stream pf / dec` rates — the speed a
  single request sees — plus `streams N` so it is clear how much concurrency
  the aggregate covers. `ttft`, `spec` and `slots` round out the card;
  speculative-decode acceptance and TTFT are re-sampled on the headline
  interval, not averaged over the process lifetime.
- Colors follow the reference nvtop-style layout: decode cyan, prefill
  magenta, KV green/yellow/red at the usual thresholds.
- Everything is laid out as fixed-width lines, so narrow terminals truncate
  cleanly instead of wrapping the layout.

## Theme

Like son-of-anton, llmtop asks the terminal for its real background/foreground
(OSC 10/11) before Textual starts, generates panel/surface/border/muted shades
from that background (opencode's `generateSystem` ramp), and leaves the base
background transparent so terminal transparency and backgrounds survive.
Accents stay on the terminal's ANSI palette, and the Textual theme is
`ansi-dark`/`ansi-light` by polarity. No answer from the terminal simply means
no generated surfaces.

`theme = "auto"` in the config is the default; set a Textual theme name
(e.g. `theme = "textual-dark"`) to pin one. `LLMTOP_NO_COLOR_QUERY=1` skips
the OSC query.

## Data sources

| Panel | Source | Notes |
| --- | --- | --- |
| Engine tok/s, queues, KV, TTFT, spec decode | engine `/metrics` (Prometheus) | vLLM and llama.cpp/gufo counters; no proxy involved |
| Active sessions | llama.cpp `/slots` | optional per engine (`slots = true`) |
| Request origins (key, app, model, tokens, latency, status) | Bifrost `GET /api/logs` | needs admin basic-auth credentials |

The tool degrades gracefully: without credentials the engines panel still
works and the origins table explains what is missing.

Origins are grouped by virtual key plus client application. The key is the
identity: give each consumer its own Bifrost virtual key and the key names
the origin regardless of where its backend or browser happens to run.

## Run it

```console
$ nix run github:ewtodd/llmtop
$ nix run github:ewtodd/llmtop -- --demo   # synthetic data, no network
$ nix run github:ewtodd/llmtop -- --dump   # poll once, print JSON, exit
```

For development:

```console
$ nix develop
$ python -m llmtop --demo
$ pytest
```

## Configuration

`~/.config/llmtop/config.toml` (or `--config`, or `$LLMTOP_CONFIG`). The full
annotated example is in [`config/llmtop.toml`](config/llmtop.toml); only the
engines you list are shown, which is why embedding/titling backends are absent
by default.

```toml
poll_interval = 1.5
headline_interval_seconds = 3.0

[gateway]
url = "http://10.0.0.6:4002"
username = "${BIFROST_ADMIN_USERNAME}"
password = "${BIFROST_ADMIN_PASSWORD}"
exclude_providers = [ "embed", "oracle" ]   # utility providers filtered out

[[engines]]
id = "vllm"
name = "qwen3.8-27b (vLLM)"
kind = "vllm"                 # vllm | llamacpp | openai
url = "http://10.0.0.5:8100"
provider = "vllm"             # Bifrost provider name, for cross-referencing

[[engines]]
id = "strix"
name = "qwen3.8-flash-next (gufo)"
kind = "llamacpp"
url = "http://10.0.0.5:8050"
provider = "strix"
slots = true
```

`${VAR}` values expand from the environment or from `env_file`
(`~/.config/llmtop/bifrost.env` by default, and `/run/agenix/bifrost-env` when
present). To enable origins on e-desktop:

```console
$ ssh -t oracle sudo cat /run/agenix/bifrost-env > ~/.config/llmtop/bifrost.env
$ chmod 600 ~/.config/llmtop/bifrost.env
```

## Keys

`q` quit, `p` pause polling, `r` refresh now, `o` cycle origin sort
(requests/tokens/latency/last), `c` clear the request feed.

## Layout

```
src/llmtop/
  config.py       TOML loading, ${VAR} expansion, defaults
  prometheus.py   Prometheus text exposition parser
  engines.py      engine scrapers + rate/recent-request accounting
  gateway.py      Bifrost /api/logs client
  origins.py      request-log rows -> origin rows
  palette.py      OSC 10/11 query + terminal-derived surface generation
  render.py       big-number glyphs, meters, formatting
  monitor.py      polls everything and produces a Snapshot
  demo.py         synthetic monitor for --demo and tests
  app.py          the Textual UI
tools/            live verification scripts (rates, pty replay, screenshot)
tests/            unit tests + Textual pilot tests
```

## Notes and limits

- The origin identity is the Bifrost virtual key plus client application.
  Give each consumer its own key; a shared key cannot be split back apart.
- Network addresses are deliberately not shown: behind a browser or a backend
  the TCP peer is the backend host (e.g. open-webui on oracle), not the human,
  which is misleading for API accounting.
- The request feed shows `processing` rows as they appear, so in-flight
  requests are visible before they complete.

## License

MIT

## Credits

- Card layout and the 5-row big-digit style follow
  [juliantsu/llmtop](https://github.com/juliantsu/llmtop) (MIT).
- The terminal palette (OSC query + generated surfaces) is ported from
  son-of-anton (MIT), which in turn follows opencode's `generateSystem`.

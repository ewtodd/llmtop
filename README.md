# llmtop

A btop/htop/nvtop-style terminal UI for self-hosted LLM inference. It answers
two questions:

1. **What are my engines doing right now?** Prompt (prefill) and generation
   (decode) tokens/s per engine, plus the per-request average over recent
   requests, running/waiting requests, KV-cache usage, TTFT and speculative
   decode acceptance.
2. **Who is hitting my API?** Requests grouped by Bifrost virtual key, client
   application, and originating machine (IP -> hostname), with token rates,
   in-flight requests, failures and latency.

Stack matches [son-of-anton](https://github.com/ewtodd/son-of-anton): Python +
[Textual](https://textual.textualize.io/) + Rich.

```
 llmtop  —  e-desktop -> http://10.0.0.6:4002                          12:35:50
 Engine                      Model                St  Run Wait  Prefill Decode ...
 qwen3.8-27b (vLLM)          qwen3.8-27b          up    3    1      423     48 ...
 qwen3.8-flash-next (gufo)   qwen3.8-flash-next   up    1    0      492     64 ...
 Key         App           Machine                  Req Live Fail Prefill Decode ...
 open-webui  open-webui    oracle      10.0.0.6     24    0    0     122     10 ...
 opencode    opencode      e-desktop   10.0.0.4     14    0    0      51     18 ...
 soa         son-of-anton  e-desktop   10.0.0.4     10    1    0      28     10 ...
 friend      unknown       203.0.113.77             23    0    0      83     19 ...
```

## Data sources

| Panel | Source | Notes |
| --- | --- | --- |
| Engine tok/s, queues, KV, TTFT, spec decode | engine `/metrics` (Prometheus) | vLLM and llama.cpp/gufo counters; no proxy involved |
| Active sessions | llama.cpp `/slots` | optional per engine (`slots = true`) |
| Request origins (key, app, model, tokens, latency, status) | Bifrost `GET /api/logs` | needs admin basic-auth credentials |
| Originating machine IP | Bifrost stdout journal over SSH (`journalctl -u bifrost -f`) | Bifrost stores remote addresses only in stdout, not in the log database; llmtop joins journal lines to log rows by request id |

The tool degrades gracefully: without credentials the engines panel still
works, without SSH the origins table shows keys/apps but no machine column.

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
avg_window_seconds = 60.0

[gateway]
url = "http://10.0.0.6:4002"
username = "${BIFROST_ADMIN_USERNAME}"
password = "${BIFROST_ADMIN_PASSWORD}"
exclude_providers = [ "embed", "oracle" ]   # utility providers filtered out
journal_enabled = true
journal_ssh = "oracle"
journal_unit = "bifrost"

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

The label column hostnames come from `[hosts]` in the config; the defaults map
the current cluster (`10.0.0.4 = e-desktop`, `10.0.0.5 = son-of-anton`,
`10.0.0.6 = oracle`, `10.0.0.7 = nu`, ...).

## Keys

`q` quit, `p` pause polling, `r` refresh now, `o` cycle origin sort
(requests/tokens/latency/last), `c` clear the request feed.

## Layout

```
src/llmtop/
  config.py       TOML loading, ${VAR} expansion, defaults
  prometheus.py   Prometheus text exposition parser
  engines.py      engine scrapers + rate/recent-request accounting
  gateway.py      Bifrost /api/logs client and SSH journal IP source
  origins.py      request-log rows -> origin rows
  monitor.py      polls everything and produces a Snapshot
  demo.py         synthetic monitor for --demo and tests
  app.py          the Textual UI
tools/            live verification scripts (rates, journal, screenshot)
tests/            unit tests + Textual pilot tests
```

## Notes and limits

- PIDs of *remote* callers do not exist on the gateway; origin is reported as
  key + application + machine IP. Local processes on the machine running
  llmtop can be resolved to PIDs incidentally via `/proc`, but keys/IPs are
  the portable identity.
- Client IPs require the journal SSH source. It uses
  `ssh -o BatchMode=yes oracle journalctl ...`; the `oracle` user must be able
  to read the unit journal (wheel members can on the current hosts).
- The request feed shows `processing` rows as they appear, so in-flight
  requests are visible before they complete.

## License

MIT

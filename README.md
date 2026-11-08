# llmtop

A btop-style terminal monitor for a self-hosted LLM stack, in one window: the
engines, the GPUs and hosts they run on, and who is calling. Point it at
[maitred](https://github.com/ewtodd/maitred) and it finds the rest; there is no
config file. It is built for a tall panel (about 115x55) and also works at 80x24
and 200x50.

## What it shows

- **Engines.** One card per serving engine: the big numbers are the
  **per-stream** prefill and decode tok/s (what one request experiences), with
  meters scaled to the engine's recent peak and a K/V meter. Below: running,
  waiting, maitred's live count, busy, TTFT, speculative acceptance, prefix-cache
  hit rate, and the aggregate across streams. An engine that goes quiet keeps its
  last speeds, dimmed, with `idle 4m`. Engines that are up but tiny collapse to
  one line; the rest are listed as `standby`. When rows are short, cards become
  two lines; from 200 columns two sit side by side.
- **Hosts and GPUs** (node_exporter): CPU, memory, load, and per GPU the busy
  share, VRAM (GTT for an iGPU), temperatures, power, fan and clock.
- **Callers**: maitred's virtual keys with live requests, 10-minute counts,
  failures, tokens, cache share and median time to first byte.
- **Feed**: the newest requests, with a live elapsed counter for streams.

A source that fails shows its error inline and keeps its last good data, dimmed,
with its age.

## Run

```
nix run github:ewtodd/llmtop -- [flags]
```

```
llmtop [--maitred URL] [--interval 1s] [--node-port 9100] [--no-hosts]
       [--engine NAME=URL]... [--gpu-name HOST/PCI=LABEL]... [--dump] [--demo]
```

- `--maitred`: default `$LLMTOP_MAITRED`, else `http://10.0.0.2:4002`.
- `--engine NAME=URL`: add an engine that is not behind maitred (server root, no
  `/v1`); repeatable.
- `--gpu-name son-of-anton/0000:c7:00.0="R9700 #0"`: label a GPU. The default is
  the driver and short PCI address (`amdgpu c7:00.0`).
- `--no-hosts`: do not read node_exporter.
- `--dump`: poll every source twice, print the normalized snapshot as JSON, exit.
- `--demo`: synthetic animated data, no network.

Keys: `q` or `ctrl+c` quit, `p` pause polling, `r` poll now.

## Data sources

All three are polled independently with an 800 ms timeout. The exact endpoints
and metric names are in [docs/contract.md](docs/contract.md).

- maitred `GET /status`: engines, modes, keys and the request feed. An older
  maitred without it falls back to `/v1/models` (model status only).
- engine `GET /metrics`: the engine kind is detected from the metric names
  (`unholy_`, `eklow_`, `radiance:`, `vllm:`, `llamacpp:`) on every scrape; with
  no metrics, `/health` says up or down.
- node_exporter `:9100/metrics` on each engine host, with the `drm` collector and
  the `drm_card_device_info` textfile metric for busy and VRAM.

## Develop

```
nix develop      # go, gopls, gcc
go test ./...
nix build && nix flake check
```

## Credits

Card layout and big digits after [juliantsu/llmtop](https://github.com/juliantsu/llmtop)
(MIT). The terminal-derived palette is from son-of-anton's TUI.

MIT licensed.

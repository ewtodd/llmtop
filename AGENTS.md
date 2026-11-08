# llmtop - Development Guide

Instructions for AI coding assistants and developers working on the llmtop codebase.

**Never give up on the right solution.**

## What llmtop Is

A btop-style terminal monitor for the house's self-hosted LLM stack: the engines
(unholy, eklow, radiance, gufo, llama.cpp), the GPUs and hosts they run on, and
the clients hitting them through [maitred](https://github.com/ewtodd/maitred).
It is one Go binary with no configuration file: point it at maitred and it finds
everything else.

- **maitred `GET /status`** - upstreams (engines), modes, per-key activity and the
  recent request feed. The engine list and the host list come from here.
- **Engine `GET /metrics`** - token counters, queues, K/V use, TTFT, speculative
  decoding. The engine kind is detected from the metric names (`unholy_*`,
  `eklow_*`, `radiance:*`, `vllm:*`, `llamacpp:*`); an engine without metrics is
  shown up or down from `/health`.
- **node_exporter `:9100/metrics`** on each engine host - GPU busy, VRAM, temps,
  power, fans, clocks; CPU and memory.

The endpoints and metric names llmtop relies on are specified in
`docs/contract.md`. unholy and eklow export their metrics for llmtop: change the
contract there and in those repos together.

Two properties shape the design:

- **The headline is the per-stream speed**: what one request experiences
  (prefill and decode tokens/s), computed live from the engines' counters. The
  aggregate across concurrent requests is secondary.
- **Nothing blocks the screen.** Every source is polled independently with short
  timeouts; a dead source shows inline, never as a crash or a frozen frame.

## Layout

- `cmd/llmtop/` - the binary (flags, `--dump`, `--demo`).
- `internal/prom/` - Prometheus text parser.
- `internal/model/` - plain data types: normalized engine readings, node scrapes, maitred's `/status`.
- `internal/source/` - maitred, engine and node_exporter clients, kind detection, discovery.
- `internal/rates/` - counter windows, per-stream and aggregate rates, host CPU and power.
- `internal/state/` - folds source messages into the `Snapshot` the UI renders and `--dump` prints.
- `internal/ui/` - the Bubble Tea program, layout, theme and `--demo` data.
- `testdata/` - recorded and synthetic endpoint bodies.

## Nix layout

- Inputs: `nixpkgs` only.
- `packages.default` (also `packages.llmtop`) - `buildGoModule`; its tests run in
  the check phase. Bumping a Go dependency changes `vendorHash`.
- `nix develop`: Go, gopls, gcc (for `-race`).
- Checks (`nix flake check`): the package build, which runs the tests.

The flake only sees files in the git index: `git add` a new file before
`nix build` or `nix flake check`.

## Gates

There is no CI. Run them yourself before committing:

```bash
test -z "$(gofmt -l .)"
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CGO_ENABLED=1 go test -race ./...
nix build && nix flake check
```

Tests never reach the network: they use `httptest` servers and `testdata/`.

### Commit convention

- Author: `son-of-anton-bot <307402699+son-of-anton-bot@users.noreply.github.com>`.
  The repo's git config may name the owner; commit with
  `git -c user.name=son-of-anton-bot -c user.email=307402699+son-of-anton-bot@users.noreply.github.com commit ...`.
  Do not change the repo's git config.
- Sole authorship. No `Co-authored-by:` trailers, for anyone - not the repo owner,
  not the model.
- Name the model. A commit made by an agent ends its body with one line,
  `Model: <id>`: the exact model that wrote the change (`Model: claude-opus-5-5`,
  `Model: claude-sonnet-5-5`). Under maitred's `auto`, name the model that actually
  served it (its `x-maitred-model` header), never `auto`.
- Message: a short summary line (`area: what changed`), a blank line, then a body that
  explains what changed and why. Never cram several changes into one long subject line.
- Remote: `git@github.com:ewtodd/llmtop.git`, branch `main`. Never push; the owner does.

## Deployment (the owner's machines)

e-workstation runs llmtop from `/etc/nixos`'s `llmtop` flake input:
`home-manager/packages/llmtop` installs it with the maitred address and GPU labels
built in, and niri's sysmon layout opens it on the `c-sysmon` workspace under btop
(`home-manager/desktopEnvironment/niri/settings/profile.nix`).
Pushing does nothing until that input is updated (`nix flake update llmtop`), the
lock committed, and e-workstation rebuilt with `nrs` (see /etc/nixos/AGENTS.md).
The `drm` collector and the `drm_card_device_info` textfile metric it reads come
from `/etc/nixos`'s node-exporter module.

## Known pitfalls

- Never block the Bubble Tea update loop on I/O; polls run in goroutines and
  deliver messages.
- Lines are laid out at a fixed width and truncated, never wrapped: a wrapped line
  breaks every row below it. Test views at narrow and short sizes.
- Counters reset when an engine restarts (and engines switch with `llm-mode`):
  a negative delta starts a new window, it is never a rate.
- Dependencies in the Go module need a reason; standard library first.

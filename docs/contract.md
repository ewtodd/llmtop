# llmtop contract (v1)

This is llmtop's contract with the endpoints it reads. It is authoritative and must stay in sync with
unholy, eklow and maitred, which implement their side of it.

llmtop is a btop-style terminal monitor for the house's LLM serving stack. It reads three kinds of
endpoint. This file is the contract between them; each task implements its side exactly.

Everything here is **additive observability**. No change may alter what a server serves, how it
schedules, samples, caches, routes or answers: the bytes a client receives stay identical.

## 1. Engine metrics (unholy, eklow)

`GET /metrics` on the engine's OpenAI port, Prometheus text exposition (`text/plain; version=0.0.4`),
no authentication (like `/health`). `P` is the engine's prefix: `unholy` or `eklow`. Names without
labels unless stated. Counters are monotonic since process start (floats allowed for seconds).

| Name | Type | Meaning |
| --- | --- | --- |
| `P_info{model="<served name>"}` | gauge, always 1 | the served model name (what `/v1/models` lists) |
| `P_slots` | gauge | sequences the engine runs at once (max concurrent requests) |
| `P_context_tokens` | gauge | the per-sequence context limit, tokens |
| `P_running` | gauge | requests being processed now |
| `P_waiting` | gauge | requests admitted to the server but queued for a slot/pages |
| `P_kv_usage_ratio` | gauge | 0..1, share of the K/V pool held by sequences (cached-but-unreferenced prefix pages count as free) |
| `P_steps_total` | counter | engine steps |
| `P_busy_seconds_total` | counter | wall seconds the engine thread spent stepping (not idle-waiting) |
| `P_prompt_tokens_total` | counter | prompt tokens of admitted requests |
| `P_cached_prompt_tokens_total` | counter | of those, tokens served from the prefix cache / snapshots (not computed) |
| `P_prefill_tokens_total` | counter | prompt tokens actually computed (fed through the model as prefill) |
| `P_decode_tokens_total` | counter | tokens generated (sampled and emitted, speculative acceptances included) |
| `P_prefill_sequence_seconds_total` | counter | **new.** Sum over sequences of the wall seconds each spent in its prefill phase while running |
| `P_decode_sequence_seconds_total` | counter | **new.** Sum over sequences of the wall seconds each spent in its decode phase while running |
| `P_first_token_seconds_total` | counter | **new.** Sum over requests of (first generated token handed to the HTTP layer − request arrival at the engine) |
| `P_first_tokens_total` | counter | **new.** Requests counted in the line above |
| `P_spec_verify_steps_total` | counter | speculative verify blocks (0 when no drafter) |
| `P_spec_drafted_total` | counter | draft tokens proposed |
| `P_spec_accepted_total` | counter | draft tokens accepted |
| `P_http_requests_total` | counter | HTTP requests handled on the inference routes |
| `P_http_errors_total` | counter | of those, answered with an error status |

The two `*_sequence_seconds_total` counters exist so a client can compute the **per-stream** speed
(what one request experiences) live, every scrape, not only when requests finish:

```
per-stream decode tok/s  = Δ decode_tokens_total  / Δ decode_sequence_seconds_total
per-stream prefill tok/s = Δ prefill_tokens_total / Δ prefill_sequence_seconds_total
aggregate decode tok/s   = Δ decode_tokens_total  / Δ wall
```

So each must be accounted consistently with the token counter it pairs with: a sequence's time counts
toward exactly one phase per step (the phase whose tokens it contributed in that step); a sequence that
contributed both in one step (e.g. the last prompt chunk plus its first sampled token) counts toward
prefill. Time a request spends queued (`waiting`) counts toward neither. Time is accumulated per step
(or per engine-loop iteration) as `dt × number of sequences in that phase`, using the same `dt` the
engine already uses for `busy_seconds`, so the counters advance continuously.

Existing metric names (unholy has more: `unholy_free_pages`, `unholy_preemptions_total`, ...) stay
exactly as they are. Lines may appear in any order.

## 2. maitred `GET /status`

LAN-only by deployment (the WAN reverse proxy forwards only `/v1*` and `/mcp*`); no authentication.
`200`, `application/json`. Never blocks on an upstream for more than the existing probe timeout (use the
same cached probe snapshot `/v1/models` uses). Times are Unix seconds as floats; durations are integer
milliseconds.

```json
{
  "version": 1,
  "now": 1762590000.123,
  "started": 1762500000.0,
  "upstreams": [
    {
      "name": "unholy-glm",
      "base_url": "http://10.0.0.5:8250/v1",
      "host": "son-of-anton",
      "hosted": false,
      "serving": true,
      "reason": "",
      "in_flight": 3,
      "slots": 8,
      "models": [ { "id": "unholy/glm5.3-flash", "display_name": "GLM-5.3-Flash", "hidden": false, "tier": 3 } ]
    }
  ],
  "modes": [
    { "name": "glm", "label": "GLM-5.3-Flash", "host": "son-of-anton", "upstreams": ["unholy-glm"], "serving": true }
  ],
  "keys": [
    {
      "name": "son-of-anton",
      "in_flight": 2,
      "requests": 120,
      "failures": 1,
      "prompt_tokens": 812345,
      "completion_tokens": 40321,
      "cached_tokens": 790000,
      "last": 1762589990.5
    }
  ],
  "recent": [
    {
      "id": 4711,
      "start": 1762589990.5,
      "key": "son-of-anton",
      "requested": "auto",
      "model": "unholy/glm5.3-flash",
      "upstream": "unholy-glm",
      "route": "<router decision reason, empty when the model was named>",
      "path": "chat/completions",
      "status": 200,
      "headers_ms": 12,
      "first_chunk_ms": 3,
      "first_token_ms": 840,
      "duration_ms": null,
      "done": false,
      "error": "",
      "usage": null
    }
  ]
}
```

Field rules:

- `upstreams`: every configured upstream, in config order. `host`: the `host` of the first mode that
  lists the upstream, else `null`. `hosted`: true when the upstream has `api_key_env` (a hosted API, not a
  LAN engine). `serving`/`reason`: the cached probe's verdict for that upstream (`reason` empty when
  serving); a hosted upstream is not probed by `/status` and reports `serving: null`, `reason: ""`.
  Config order is the JSON file's, which is alphabetical when Nix writes it. `in_flight`: requests maitred currently has open to it (the existing activity counter).
  `slots`: the configured value or `null`. `models`: the configured models on that upstream.
- `modes`: config order; `serving` is true when every upstream of the mode is serving.
- `keys`: every configured key, config order, counted since maitred started. `failures`: requests that
  ended with status ≥ 400 or a relay error. Token fields sum the `usage` of finished requests that had one.
  `last`: start time of its latest request, or `null`.
- `recent`: the most recent requests, **newest first**, at most 512 kept (a ring). A request is recorded
  once its key is identified (unauthenticated requests are not recorded) and updated in place as it
  progresses; it appears while still streaming (`done: false`).
  - `model`/`upstream`: the model actually picked (for a routed id, the routed decision); for a request
    rejected before a model was picked (404/403/429/503 no-route), `model` is the requested id and
    `upstream` is `""`.
  - `status`: the HTTP status sent to the client (0 until known).
  - `headers_ms`: request arrival → upstream response headers (null if never reached).
  - `first_chunk_ms`: arrival → first body chunk relayed to the client (null until it happens). Servers send
    an empty role chunk as soon as they accept a stream, so this is not the time to the first token.
  - `first_token_ms`: arrival → first relayed chunk carrying generated output (a non-empty `content`,
    `reasoning_content`, `reasoning`, `text` or Responses `delta` string, or a non-empty `tool_calls`
    array), found by scanning chunks until it appears (null until then). llmtop's caller TTFT.
  - `duration_ms`: arrival → end of the relayed body (null while streaming).
  - `done`: the response is over (body ended, failed, or the client went away).
  - `error`: empty on success; `"client closed"` when the client dropped the body early; the relay
    error text on an upstream failure/idle timeout; the error `code` for maitred's own error answers.
  - `usage`: when the upstream's answer contained an OpenAI `usage` object (the last one in the body;
    SSE streams carry it in their final chunk when the client asked for `stream_options.include_usage`):
    `{"prompt_tokens": n, "completion_tokens": n, "cached_tokens": n}` where `cached_tokens` is
    `usage.prompt_tokens_details.cached_tokens` or `0`. Otherwise `null`. Found by scanning a bounded tail
    (e.g. the last 16 KiB) of the relayed bytes, never by buffering the whole body; the bytes forwarded to
    the client are untouched and not delayed.

## 3. Host metrics (node_exporter, port 9100)

Already deployed on every host. llmtop reads `node_hwmon_*` (GPU temps, power, fans, clocks; chips are
labelled `<bridge>_<device>` with `.` written `_`, e.g. `0000:cb:00_0_0000:cc:00_0` for device
`0000:cc:00.0`), `node_cpu_seconds_total`, `node_memory_MemTotal_bytes`, `node_memory_MemAvailable_bytes`.
A sensor's critical limit, `node_hwmon_temp_crit_celsius{chip,sensor}` when the driver exports it
(amdgpu does: 110 °C edge and junction, 108 °C memory on the R9700s), sets that temperature's
warning colours: yellow 20° under the limit, red 10° under.
To be deployed with llmtop (in /etc/nixos): the `drm` collector (`node_drm_gpu_busy_percent{card}`,
`node_drm_memory_{vram,gtt}_{size,used}_bytes{card}`, amdgpu only) and a textfile metric mapping DRM
cards to PCI devices: `drm_card_device_info{card="card1",device="0000:cc:00.0",driver="amdgpu"} 1`.

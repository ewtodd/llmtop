"""Poll the configured engines a few times and print live rates."""

import time

from llmtop.config import default_config
from llmtop.engines import EngineRunner

config = default_config()
runners = [ EngineRunner(engine, config.avg_window_seconds, config.history_seconds) for engine in config.engines ]

for _ in range(3):
    states = [ runner.poll() for runner in runners ]
    time.sleep(3)

for state in states:
    print(
        f"{state.id}: ok={state.ok} err={state.error!r} "
        f"prefill_now={state.prefill_now:.1f} decode_now={state.decode_now:.1f} "
        f"prefill_avg={state.prefill_avg:.1f} decode_avg={state.decode_avg:.1f} "
        f"run={state.running} wait={state.waiting} slots={state.active_slots}/{state.total_slots} "
        f"ttft={state.ttft_ms} accept={state.spec_accept}"
    )

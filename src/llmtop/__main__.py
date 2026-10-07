"""Command-line entry point."""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import asdict
from pathlib import Path

from .app import LlmTopApp
from .config import load_config
from .demo import DemoMonitor
from .monitor import LiveMonitor
from .palette import query_terminal_colors


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="llmtop",
        description="btop-style TUI for local inference engines and the clients hitting them",
    )
    parser.add_argument("--config", type=Path, default=None, help="path to llmtop.toml")
    parser.add_argument("--env-file", type=Path, default=None, help="key=value file for ${...} expansion")
    parser.add_argument("--demo", action="store_true", help="run with synthetic data, no network")
    parser.add_argument("--dump", action="store_true", help="poll once, print the snapshot as JSON, exit")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    config = load_config(args.config, args.env_file)
    monitor = DemoMonitor(config) if args.demo else LiveMonitor(config)
    try:
        if args.dump:
            snapshot = monitor.snapshot(force=True)
            json.dump(asdict(snapshot), sys.stdout, indent=2)
            print()
            return 0
        terminal_colors = query_terminal_colors()
        LlmTopApp(config, monitor=monitor, terminal_colors=terminal_colors).run()
        return 0
    finally:
        monitor.close()


if __name__ == "__main__":
    raise SystemExit(main())

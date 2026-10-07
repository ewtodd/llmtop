"""Configuration loading for llmtop.

The file format is TOML. Anything omitted falls back to the built-in defaults,
which describe the e-desktop -> Bifrost -> son-of-anton cluster.
"""

from __future__ import annotations

import os
import tomllib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

DEFAULT_CONFIG_PATHS = (
    Path("~/.config/llmtop/config.toml"),
    Path("~/.config/llmtop/llmtop.toml"),
)

VALID_ENGINE_KINDS = ("vllm", "llamacpp", "openai")


@dataclass
class EngineConfig:
    id: str
    name: str
    kind: str
    url: str
    provider: str = ""
    models: list[str] = field(default_factory=list)
    slots: bool = True
    enabled: bool = True

    def __post_init__(self) -> None:
        if self.kind not in VALID_ENGINE_KINDS:
            raise ValueError(
                f"engine {self.id!r}: unknown kind {self.kind!r}; expected one of {VALID_ENGINE_KINDS}"
            )


@dataclass
class GatewayConfig:
    enabled: bool = True
    url: str = "http://10.0.0.6:4002"
    username: str = "${BIFROST_ADMIN_USERNAME}"
    password: str = "${BIFROST_ADMIN_PASSWORD}"
    include_providers: list[str] = field(default_factory=list)
    exclude_providers: list[str] = field(default_factory=lambda: [ "embed", "oracle" ])
    log_limit: int = 200
    journal_enabled: bool = True
    journal_ssh: str = "oracle"
    journal_unit: str = "bifrost"
    journal_since: str = "10 minutes ago"


@dataclass
class Config:
    engines: list[EngineConfig] = field(default_factory=list)
    gateway: GatewayConfig = field(default_factory=GatewayConfig)
    hosts: dict[str, str] = field(default_factory=dict)
    poll_interval: float = 1.5
    gateway_poll_interval: float = 2.0
    avg_window_seconds: float = 60.0
    history_seconds: float = 180.0
    origin_window_seconds: float = 300.0
    env_file: str = "~/.config/llmtop/bifrost.env"
    theme: str = "auto"
    source_path: str = ""

    def host_name(self, address: str) -> str:
        if not address:
            return ""
        return self.hosts.get(address, "")


def default_config() -> Config:
    return Config(
        engines=[
            EngineConfig(
                id="vllm",
                name="qwen3.8-27b (vLLM)",
                kind="vllm",
                url="http://10.0.0.5:8100",
                provider="vllm",
            ),
            EngineConfig(
                id="strix",
                name="qwen3.8-flash-next (gufo)",
                kind="llamacpp",
                url="http://10.0.0.5:8050",
                provider="strix",
            ),
        ],
        gateway=GatewayConfig(),
        hosts={
            "10.0.0.2": "mu",
            "10.0.0.3": "anton",
            "10.0.0.4": "e-desktop",
            "10.0.0.5": "son-of-anton",
            "10.0.0.6": "oracle",
            "10.0.0.7": "nu",
            "10.0.0.8": "tony",
            "127.0.0.1": "oracle",
            "::1": "oracle",
        },
    )


def default_config_path() -> Path | None:
    from_env = os.environ.get("LLMTOP_CONFIG")
    if from_env:
        return Path(from_env).expanduser()
    for candidate in DEFAULT_CONFIG_PATHS:
        path = candidate.expanduser()
        if path.is_file():
            return path
    return None


def load_config(path: Path | None = None, env_file: Path | None = None) -> Config:
    config = default_config()
    resolved_path = path if path is not None else default_config_path()
    if resolved_path is not None:
        if not resolved_path.is_file():
            raise FileNotFoundError(f"config file not found: {resolved_path}")
        with resolved_path.open("rb") as handle:
            raw = tomllib.load(handle)
        config = _apply_raw(config, raw)
        config.source_path = str(resolved_path)
    environ = _load_environment(config, env_file)
    _expand_config(config, environ)
    return config


def _apply_raw(config: Config, raw: dict[str, Any]) -> Config:
    scalar_keys = (
        "poll_interval",
        "gateway_poll_interval",
        "avg_window_seconds",
        "history_seconds",
        "origin_window_seconds",
        "env_file",
        "theme",
    )
    for key in scalar_keys:
        if key in raw:
            setattr(config, key, raw[key])

    if "engines" in raw:
        config.engines = [_engine_from_raw(item) for item in raw["engines"]]
    if "hosts" in raw:
        config.hosts = {str(key): str(value) for key, value in raw["hosts"].items()}
        config.hosts.setdefault("127.0.0.1", "oracle")
        config.hosts.setdefault("::1", "oracle")

    gateway_raw = raw.get("gateway", {})
    if isinstance(gateway_raw, dict):
        for key, value in gateway_raw.items():
            if hasattr(config.gateway, key):
                setattr(config.gateway, key, value)
    return config


def _engine_from_raw(raw: dict[str, Any]) -> EngineConfig:
    return EngineConfig(
        id=str(raw["id"]),
        name=str(raw.get("name", raw["id"])),
        kind=str(raw.get("kind", "openai")),
        url=str(raw["url"]).rstrip("/"),
        provider=str(raw.get("provider", raw.get("id", ""))),
        models=[str(item) for item in raw.get("models", [])],
        slots=bool(raw.get("slots", True)),
        enabled=bool(raw.get("enabled", True)),
    )


def _load_environment(config: Config, env_file: Path | None) -> dict[str, str]:
    environ = dict(os.environ)
    candidates = [ env_file if env_file is not None else Path(config.env_file).expanduser() ]
    # Convenience when running on the gateway host as root.
    candidates.append(Path("/run/agenix/bifrost-env"))
    for candidate in candidates:
        if not candidate.is_file():
            continue
        for line in candidate.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            environ.setdefault(key.strip(), value)
    return environ


def _expand_config(config: Config, environ: dict[str, str]) -> None:
    config.env_file = str(Path(_expand(config.env_file, environ)).expanduser())
    for engine in config.engines:
        engine.url = _expand(engine.url, environ)
        engine.provider = _expand(engine.provider, environ)
        engine.name = _expand(engine.name, environ)
    gateway = config.gateway
    gateway.url = _expand(gateway.url, environ).rstrip("/")
    gateway.username = _expand(gateway.username, environ)
    gateway.password = _expand(gateway.password, environ)
    gateway.journal_ssh = _expand(gateway.journal_ssh, environ)
    gateway.journal_unit = _expand(gateway.journal_unit, environ)


def _expand(value: str, environ: dict[str, str]) -> str:
    result: list[str] = []
    index = 0
    while index < len(value):
        if value[index] == "$" and index + 1 < len(value) and value[index + 1] == "{":
            end = value.find("}", index + 2)
            if end < 0:
                result.append(value[index:])
                break
            expression = value[index + 2 : end]
            name, separator, fallback = expression.partition(":-")
            replacement = environ.get(name)
            if replacement is None:
                replacement = fallback if separator else ""
            result.append(replacement)
            index = end + 1
            continue
        result.append(value[index])
        index += 1
    return "".join(result)

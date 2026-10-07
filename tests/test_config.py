from pathlib import Path

import pytest

from llmtop.config import EngineConfig, default_config, load_config


def test_defaults_focus_on_chat_engines():
    config = default_config()
    assert [ engine.id for engine in config.engines ] == [ "vllm", "strix" ]
    assert "embed" in config.gateway.exclude_providers
    assert "oracle" in config.gateway.exclude_providers


def test_engine_rejects_unknown_kind():
    with pytest.raises(ValueError):
        EngineConfig(id="x", name="x", kind="banana", url="http://x")


def test_load_toml_overrides(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setenv("LLMTOP_TEST_HOST", "engine.local")
    config_path = tmp_path / "llmtop.toml"
    config_path.write_text(
        """
poll_interval = 0.5

[[engines]]
id = "x"
name = "X"
kind = "openai"
url = "http://${LLMTOP_TEST_HOST}:9999/"
provider = "x"

[gateway]
url = "http://gw.local:4002/"
exclude_providers = [ "nope" ]
""".strip()
    )
    config = load_config(config_path, env_file=tmp_path / "missing.env")
    assert config.poll_interval == 0.5
    assert [ engine.url for engine in config.engines ] == [ "http://engine.local:9999" ]
    assert config.gateway.url == "http://gw.local:4002"
    assert config.gateway.exclude_providers == [ "nope" ]
    assert config.source_path == str(config_path)


def test_env_file_feeds_expansion(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.delenv("BIFROST_ADMIN_USERNAME", raising=False)
    monkeypatch.delenv("BIFROST_ADMIN_PASSWORD", raising=False)
    env_file = tmp_path / "bifrost.env"
    env_file.write_text(
        "\n".join(
            [
                "# comment",
                "BIFROST_ADMIN_USERNAME=admin",
                'BIFROST_ADMIN_PASSWORD="s3cret"',
                "",
            ]
        )
    )
    config = load_config(None, env_file=env_file)
    assert config.gateway.username == "admin"
    assert config.gateway.password == "s3cret"


def test_missing_expansion_degrades_to_empty(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.delenv("BIFROST_ADMIN_USERNAME", raising=False)
    monkeypatch.delenv("BIFROST_ADMIN_PASSWORD", raising=False)
    config = load_config(None, env_file=tmp_path / "missing.env")
    assert config.gateway.username == ""
    assert config.gateway.password == ""

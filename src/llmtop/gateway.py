"""Bifrost gateway access: request logs and the stdout journal for client IPs."""

from __future__ import annotations

import base64
import json
import shlex
import subprocess
import threading
import time
import urllib.error
import urllib.request
from collections import OrderedDict
from dataclasses import dataclass

from .config import GatewayConfig

USER_AGENT = "llmtop/0.1"
JOURNAL_CAPACITY = 20000


class GatewayError(RuntimeError):
    pass


@dataclass
class JournalEntry:
    request_id: str
    timestamp: float
    address: str
    target: str
    user_agent: str
    status_code: int
    duration_ms: float


class BifrostClient:
    def __init__(self, config: GatewayConfig, timeout: float = 3.0) -> None:
        self.config = config
        self.timeout = timeout

    @property
    def has_credentials(self) -> bool:
        return bool(self.config.username and self.config.password)

    @property
    def enabled(self) -> bool:
        return self.config.enabled and bool(self.config.url)

    def health(self) -> tuple[bool, str]:
        if not self.enabled:
            return False, "disabled"
        try:
            self._get("/health", authenticated=False)
            return True, "up"
        except GatewayError as error:
            return False, str(error)

    def fetch_logs(self) -> list[dict]:
        if not self.has_credentials:
            raise GatewayError("admin credentials not configured")
        query = f"/api/logs?limit={self.config.log_limit}&sort_by=timestamp&order=desc"
        payload = self._get(query, authenticated=True)
        try:
            data = json.loads(payload)
        except json.JSONDecodeError as error:
            raise GatewayError(f"invalid JSON: {error}") from error
        logs = data.get("logs")
        if not isinstance(logs, list):
            raise GatewayError("response missing logs array")
        return logs

    def _get(self, path: str, authenticated: bool) -> bytes:
        url = f"{self.config.url}{path}"
        headers = {"User-Agent": USER_AGENT}
        if authenticated:
            token = base64.b64encode(
                f"{self.config.username}:{self.config.password}".encode("utf-8")
            ).decode("ascii")
            headers["Authorization"] = f"Basic {token}"
        request = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                return response.read()
        except urllib.error.HTTPError as error:
            if error.code == 401:
                raise GatewayError("unauthorized (check admin credentials)") from error
            raise GatewayError(f"HTTP {error.code}") from error
        except urllib.error.URLError as error:
            raise GatewayError(f"unreachable: {error.reason}") from error
        except TimeoutError as error:
            raise GatewayError("timeout") from error


class JournalIPSource:
    """Follows the gateway's journald output over SSH and indexes client IPs.

    Bifrost stores the remote address only in its stdout logs, not in the log
    database, so the journal line is the join key between a request id and the
    machine that originated it.
    """

    def __init__(self, config: GatewayConfig) -> None:
        self.config = config
        self.status = "off"
        self._entries: OrderedDict[str, JournalEntry] = OrderedDict()
        self._lock = threading.Lock()
        self._process: subprocess.Popen[str] | None = None
        self._reader: threading.Thread | None = None

    def start(self) -> None:
        if not self.config.journal_enabled or not self.config.journal_ssh:
            self.status = "off"
            return
        # ssh flattens the remote argv with spaces, so the remote command is
        # built as one shell-quoted string and sent as a single argument.
        remote_command = shlex.join(
            [
                "journalctl",
                "-u",
                self.config.journal_unit,
                "-f",
                "-o",
                "cat",
                "--no-pager",
                "--since",
                self.config.journal_since,
            ]
        )
        command = [
            "ssh",
            "-o",
            "BatchMode=yes",
            "-o",
            "ConnectTimeout=5",
            self.config.journal_ssh,
            remote_command,
        ]
        self.status = "starting"
        try:
            self._process = subprocess.Popen(
                command,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
            )
        except OSError as error:
            self.status = f"error: {error}"
            return
        self._reader = threading.Thread(target=self._read_lines, daemon=True)
        self._reader.start()

    def stop(self) -> None:
        process = self._process
        if process is None:
            return
        process.terminate()
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            process.kill()
        self._process = None

    def lookup(self, request_id: str) -> JournalEntry | None:
        if not request_id:
            return None
        with self._lock:
            return self._entries.get(request_id)

    def summary(self) -> str:
        if self.status == "live":
            with self._lock:
                return f"live ({len(self._entries)} requests)"
        return self.status

    def _read_lines(self) -> None:
        assert self._process is not None and self._process.stdout is not None
        live_seen = False
        for line in self._process.stdout:
            line = line.strip()
            if not line:
                continue
            if not line.startswith("{"):
                if not live_seen:
                    self.status = f"error: {line[:120]}"
                continue
            entry = _parse_journal_line(line)
            if entry is None:
                continue
            with self._lock:
                self._entries[entry.request_id] = entry
                self._entries.move_to_end(entry.request_id)
                while len(self._entries) > JOURNAL_CAPACITY:
                    self._entries.popitem(last=False)
            if not live_seen:
                live_seen = True
                self.status = "live"
        if self.status == "starting":
            self.status = "error: journal stream ended"


def _parse_journal_line(line: str) -> JournalEntry | None:
    try:
        record = json.loads(line)
    except json.JSONDecodeError:
        return None
    if record.get("message") != "request completed":
        return None
    request_id = record.get("request_id")
    remote = record.get("http.remote_addr")
    if not request_id or not remote:
        return None
    address, _, _ = remote.rpartition(":")
    address = address.strip("[]") or remote
    return JournalEntry(
        request_id=request_id,
        timestamp=_parse_time(record.get("time")),
        address=address,
        target=str(record.get("http.target", "")),
        user_agent=str(record.get("http.user_agent", "")),
        status_code=int(record.get("http.status_code", 0) or 0),
        duration_ms=float(record.get("http.request_duration_ms", 0.0) or 0.0),
    )


def _parse_time(raw: object) -> float:
    if not isinstance(raw, str) or not raw:
        return time.time()
    try:
        from datetime import datetime

        return datetime.fromisoformat(raw).timestamp()
    except ValueError:
        return time.time()


def ssh_command_preview(config: GatewayConfig) -> str:
    return shlex.join(
        [
            "ssh",
            config.journal_ssh,
            "journalctl",
            "-u",
            config.journal_unit,
            "-f",
            "-o",
            "cat",
        ]
    )

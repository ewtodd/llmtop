"""Bifrost gateway access."""

from __future__ import annotations

import base64
import json
import urllib.error
import urllib.request

from .config import GatewayConfig

USER_AGENT = "llmtop/0.1"


class GatewayError(RuntimeError):
    pass


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

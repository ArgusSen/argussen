"""A small async client for ArgusSen's API."""

from __future__ import annotations

from collections.abc import AsyncIterator
import json
from typing import Any

import aiohttp


class ArgusSenError(Exception):
    """ArgusSen could not be reached or answered with an error."""


class ArgusSenAuthError(ArgusSenError):
    """The token or the login is not accepted."""


class ArgusSenClient:
    """Talks to one ArgusSen server (the Home Assistant app, or a separate server)."""

    def __init__(self, session: aiohttp.ClientSession, url: str, token: str) -> None:
        self._session = session
        self.url = url.rstrip("/")
        self.token = token

    @property
    def _headers(self) -> dict[str, str]:
        return {"Authorization": f"Bearer {self.token}"}

    @staticmethod
    async def login(session: aiohttp.ClientSession, url: str, username: str, password: str) -> str:
        """Log in with a username and password and return a token."""
        try:
            async with session.post(
                url.rstrip("/") + "/api/login",
                json={"user": username, "password": password, "device": "Home Assistant integration"},
                timeout=aiohttp.ClientTimeout(total=15),
            ) as resp:
                if resp.status in (401, 403):
                    raise ArgusSenAuthError
                data = await resp.json(content_type=None)
                if resp.status != 200 or not data.get("token"):
                    raise ArgusSenAuthError if resp.status == 400 else ArgusSenError(data.get("error"))
                return data["token"]
        except (aiohttp.ClientError, TimeoutError) as err:
            raise ArgusSenError(str(err)) from err

    async def _request(self, method: str, path: str, body: Any = None, timeout: float = 15) -> Any:
        try:
            async with self._session.request(
                method, self.url + path, headers=self._headers, json=body,
                timeout=aiohttp.ClientTimeout(total=timeout),
            ) as resp:
                if resp.status == 401:
                    raise ArgusSenAuthError
                if resp.status >= 400:
                    raise ArgusSenError(f"HTTP {resp.status} for {path}")
                if resp.content_type == "application/json":
                    return await resp.json()
                return await resp.read()
        except (aiohttp.ClientError, TimeoutError) as err:
            raise ArgusSenError(str(err)) from err

    async def me(self) -> dict[str, Any]:
        """The user, the server's version and its instance id."""
        return await self._request("GET", "/api/me")

    async def cameras(self) -> list[dict[str, Any]]:
        """The cameras the user may see."""
        return await self._request("GET", "/api/cams")

    async def events(self, limit: int = 50, cam: str | None = None) -> list[dict[str, Any]]:
        """The newest events."""
        q = f"?limit={limit}" + (f"&cam={cam}" if cam else "")
        return await self._request("GET", "/api/events" + q)

    async def snapshot(self, cam: str) -> bytes:
        """The newest still image of a camera (JPEG)."""
        return await self._request("GET", f"/api/cams/{cam}/snapshot.jpg")

    async def thumbnail(self, event_id: int) -> bytes:
        """The picture of an event (JPEG)."""
        return await self._request("GET", f"/api/events/{event_id}/thumb.jpg")

    async def set_home(self, home: bool) -> None:
        """Switch the home mode on or off (administrators)."""
        await self._request("PUT", "/api/mode", {"home": home})

    async def protect_event(self, event_id: int, protect: bool) -> None:
        """Protect an event (never deleted) or remove the protection (administrators)."""
        await self._request("POST", f"/api/events/{event_id}/keep", {"keep": protect})

    def live_url(self, cam: str, stream: str = "main") -> str:
        """The live video (HLS) with the token in the address – for Home Assistant's video component."""
        return f"{self.url}/api/live/{cam}/{stream}/index.m3u8?token={self.token}"

    def clip_request(self, event_id: int) -> tuple[str, dict[str, str]]:
        """The address and headers for an event's video clip (MP4)."""
        return f"{self.url}/api/events/{event_id}/clip.mp4", self._headers

    async def stream(self) -> AsyncIterator[tuple[str, dict[str, Any]]]:
        """Changes as they happen (server-sent events): yields (kind, data) until the connection ends."""
        try:
            async with self._session.get(
                self.url + "/api/stream", headers=self._headers,
                timeout=aiohttp.ClientTimeout(total=None, sock_connect=15, sock_read=60),
            ) as resp:
                if resp.status == 401:
                    raise ArgusSenAuthError
                if resp.status != 200:
                    raise ArgusSenError(f"HTTP {resp.status} for /api/stream")
                kind, data = "", []
                async for raw in resp.content:
                    line = raw.decode("utf-8").rstrip("\r\n")
                    if line.startswith("event:"):
                        kind = line[6:].strip()
                    elif line.startswith("data:"):
                        data.append(line[5:].strip())
                    elif line == "" and data:
                        try:
                            yield kind, json.loads("\n".join(data))
                        except ValueError:
                            pass
                        kind, data = "", []
        except (aiohttp.ClientError, TimeoutError) as err:
            raise ArgusSenError(str(err)) from err

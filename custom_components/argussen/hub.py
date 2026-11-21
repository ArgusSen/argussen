"""Keeps the state of one ArgusSen server up to date (push via /api/stream, with reconnect)."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
import logging
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.dispatcher import async_dispatcher_send

from .api import ArgusSenAuthError, ArgusSenClient, ArgusSenError
from .const import BUS_EVENT, DOMAIN, signal_update

_LOGGER = logging.getLogger(__name__)


@dataclass
class CamState:
    """What is known about one camera."""

    id: str
    name: str
    online: bool = False
    motion: bool = False
    ongoing: dict[int, set[str]] = field(default_factory=dict)  # event id -> objects, while the event lasts
    latest: dict[str, Any] | None = None  # the newest event

    @property
    def objects(self) -> set[str]:
        """The objects seen in ongoing events."""
        out: set[str] = set()
        for labels in self.ongoing.values():
            out |= labels
        return out


class ArgusSenHub:
    """One ArgusSen server: its cameras, events, home mode and storage."""

    def __init__(self, hass: HomeAssistant, entry: ConfigEntry, client: ArgusSenClient, me: dict[str, Any]) -> None:
        self.hass = hass
        self.entry = entry
        self.client = client
        self.version: str = me.get("version", "")
        self.instance: str = me.get("instance", "")
        self.admin: bool = bool((me.get("user") or {}).get("admin"))
        self.cams: dict[str, CamState] = {}
        self.home: bool | None = None
        self.storage_low = False
        self.connected = False
        self._seen: dict[int, set[str]] = {}  # objects already reported per event (event entities/bus)
        self._task: asyncio.Task | None = None

    async def async_setup(self) -> None:
        """Read the cameras and the newest events, then start listening for changes."""
        for c in await self.client.cameras():
            self.cams[c["id"]] = CamState(c["id"], c["name"], c.get("online", False), c.get("motion", False))
        for e in reversed(await self.client.events(limit=100)):
            cam = self.cams.get(e["cam"])
            if cam is None:
                continue
            self._seen[e["id"]] = set(e.get("labels") or [])
            if cam.latest is None or e["start"] >= cam.latest["start"]:
                cam.latest = e
        self._task = self.entry.async_create_background_task(self.hass, self._listen(), f"{DOMAIN} stream")

    async def async_stop(self) -> None:
        """Stop listening."""
        if self._task:
            self._task.cancel()

    async def _listen(self) -> None:
        delay = 1
        while True:
            try:
                async for kind, data in self.client.stream():
                    delay = 1
                    self._handle(kind, data)
            except ArgusSenAuthError:
                _LOGGER.warning("ArgusSen no longer accepts the login – reconfigure the integration")
                self.entry.async_start_reauth(self.hass)
                return
            except ArgusSenError as err:
                _LOGGER.debug("ArgusSen stream ended: %s", err)
            except asyncio.CancelledError:
                raise
            except Exception:  # noqa: BLE001 – keep listening whatever happens
                _LOGGER.exception("ArgusSen stream failed")
            if self.connected:
                self.connected = False
                self._changed()
            await asyncio.sleep(delay)
            delay = min(delay * 2, 60)

    @callback
    def _changed(self) -> None:
        async_dispatcher_send(self.hass, signal_update(self.entry.entry_id))

    @callback
    def _handle(self, kind: str, data: dict[str, Any]) -> None:
        if kind == "state":
            ids = {c["id"] for c in data.get("cams", [])}
            if ids != set(self.cams):
                # cameras were added or removed in ArgusSen: set the integration up again
                self.hass.config_entries.async_schedule_reload(self.entry.entry_id)
                return
            for c in data.get("cams", []):
                cam = self.cams[c["id"]]
                cam.online, cam.motion, cam.name = c.get("online", False), c.get("motion", False), c.get("name", cam.name)
            self.home = data.get("home")
            self.storage_low = bool(data.get("storage_low"))
            self.connected = True
        elif kind == "cam":
            cam = self.cams.get(data.get("id"))
            if cam is None:
                self.hass.config_entries.async_schedule_reload(self.entry.entry_id)
                return
            cam.online, cam.motion = data.get("online", False), data.get("motion", False)
        elif kind == "home":
            self.home = bool(data.get("home"))
        elif kind == "storage":
            self.storage_low = bool(data.get("storage_low"))
        elif kind == "event":
            self._event(data)
        self._changed()

    @callback
    def _event(self, e: dict[str, Any]) -> None:
        cam = self.cams.get(e.get("cam"))
        if cam is None:
            return
        labels = set(e.get("labels") or [])
        if e.get("ongoing"):
            cam.ongoing[e["id"]] = labels
        else:
            cam.ongoing.pop(e["id"], None)
        if cam.latest is None or e["start"] >= cam.latest["start"]:
            cam.latest = e
        new = labels - self._seen.get(e["id"], set())
        self._seen[e["id"]] = labels
        if len(self._seen) > 500:  # forget old events
            for old in sorted(self._seen)[:-200]:
                del self._seen[old]
        # Home Assistant's event bus: every start, change and end – for automations
        self.hass.bus.async_fire(BUS_EVENT, {
            "entry_id": self.entry.entry_id, "camera": cam.id, "camera_name": cam.name, "event_id": e["id"],
            "labels": sorted(labels), "new_labels": sorted(new), "ongoing": bool(e.get("ongoing")),
            "start": e.get("start"), "end": None if e.get("ongoing") else e.get("end"),
        })
        if new:
            async_dispatcher_send(self.hass, f"{signal_update(self.entry.entry_id)}_event", cam.id, sorted(new), e)

"""Keeps the state of one ArgusSen server up to date (push via /api/stream, with reconnect)."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
import logging
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.dispatcher import async_dispatcher_send

from .api import ArgusSenAuthError, ArgusSenClient, ArgusSenError
from .const import BUS_EVENT, DOMAIN, signal_update
from .restart import async_check_files

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
    zones: dict[str, str] = field(default_factory=dict)  # zone id -> name (not the areas to ignore)
    ongoing_zones: dict[int, set[str]] = field(default_factory=dict)  # event id -> the zones it is in, while it lasts

    def zone_objects(self, zone: str) -> set[str]:
        """The objects of the ongoing events in a zone."""
        out: set[str] = set()
        for eid, zones in self.ongoing_zones.items():
            if zone in zones:
                out |= self.ongoing.get(eid, set())
        return out

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
        self.cams: dict[str, CamState] = {}  # the cameras that are switched on
        self.all_cams: dict[str, CamState] = {}  # all cameras, also those switched off (administrators only)
        self.enabled: dict[str, bool] = {}
        self.home: bool | None = None
        self.storage_low = False
        self.connected = False
        self._was_connected = False
        self._seen: dict[int, set[str]] = {}  # objects already reported per event (event entities/bus)
        self._seen_zone: dict[tuple[int, str], set[str]] = {}
        self._devices: dict[str, str] = {}  # camera id -> device id  # … per event and zone
        self._task: asyncio.Task | None = None

    async def async_setup(self) -> None:
        """Read the cameras and the newest events, then start listening for changes."""
        for c in await self.client.cameras():
            self.cams[c["id"]] = CamState(c["id"], c["name"], c.get("online", False), c.get("motion", False),
                                          zones={z["id"]: z.get("name") or z["id"] for z in c.get("zones") or [] if z.get("kind") == "zone"})
        if self.admin:
            try:
                for c in await self.client.admin_cameras():
                    self.all_cams[c["id"]] = self.cams.get(c["id"]) or CamState(c["id"], c["name"])
                    self.enabled[c["id"]] = bool(c.get("enabled"))
            except ArgusSenError as err:  # an older ArgusSen: only the cameras that are switched on
                _LOGGER.debug("ArgusSen camera list: %s", err)
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
            if not self.connected and self._was_connected:
                # back after a break – the app may have been updated with a new version of this integration
                self.hass.async_create_task(async_check_files(self.hass))
            self.connected = self._was_connected = True
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
        elif kind == "cameras":
            # a camera was added, changed, switched on/off or deleted in ArgusSen: set the integration up again
            self.hass.config_entries.async_schedule_reload(self.entry.entry_id)
            return
        elif kind == "event":
            self._event(data)
        self._changed()

    @callback
    def _device_id(self, cam: str) -> str | None:
        """The id of the camera's device in Home Assistant."""
        if cam not in self._devices:
            base = self.instance or self.entry.entry_id
            device = dr.async_get(self.hass).async_get_device(identifiers={(DOMAIN, f"{base}_{cam}")})
            if device is None:
                return None
            self._devices[cam] = device.id
        return self._devices[cam]

    @callback
    def _event(self, e: dict[str, Any]) -> None:
        cam = self.cams.get(e.get("cam"))
        if cam is None:
            return
        labels = set(e.get("labels") or [])
        zones = {z for z in e.get("zones") or [] if z in cam.zones}
        if e.get("ongoing"):
            cam.ongoing[e["id"]] = labels
            cam.ongoing_zones[e["id"]] = zones
        else:
            cam.ongoing.pop(e["id"], None)
            cam.ongoing_zones.pop(e["id"], None)
        if cam.latest is None or e["start"] >= cam.latest["start"]:
            cam.latest = e
        new = labels - self._seen.get(e["id"], set())
        self._seen[e["id"]] = labels
        if len(self._seen) > 500:  # forget old events
            for old in sorted(self._seen)[:-200]:
                del self._seen[old]
            for key in [k for k in self._seen_zone if k[0] not in self._seen]:
                del self._seen_zone[key]
        # per zone: what is new in the zone (it came into the zone, or a new object was found there)
        zone_new: dict[str, list[str]] = {}
        for zone in zones:
            seen = self._seen_zone.get((e["id"], zone), set())
            if labels - seen:
                zone_new[zone] = sorted(labels - seen)
            self._seen_zone[(e["id"], zone)] = seen | labels
        # Home Assistant's event bus: every start, change and end – for automations
        self.hass.bus.async_fire(BUS_EVENT, {
            "entry_id": self.entry.entry_id, "camera": cam.id, "camera_name": cam.name, "event_id": e["id"],
            "device_id": self._device_id(cam.id),  # the camera's device (blueprints choose the camera by its device)
            "labels": sorted(labels), "new_labels": sorted(new), "ongoing": bool(e.get("ongoing")),
            "zones": sorted(zones), "zone_names": [cam.zones[z] for z in sorted(zones)],
            "zone_new": zone_new,  # zone id -> the objects that are new there (came into the zone) – for device triggers
            "start": e.get("start"), "end": None if e.get("ongoing") else e.get("end"),
        })
        if new:
            async_dispatcher_send(self.hass, f"{signal_update(self.entry.entry_id)}_event", cam.id, sorted(new), e)
        for zone, zl in zone_new.items():
            async_dispatcher_send(self.hass, f"{signal_update(self.entry.entry_id)}_zone", cam.id, zone, zl, e)

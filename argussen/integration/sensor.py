"""The time of each camera's latest event."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from homeassistant.components.sensor import SensorDeviceClass, SensorEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback
from homeassistant.util import dt as dt_util

from . import ArgusSenConfigEntry
from .entity import ArgusSenCamEntity
from .hub import ArgusSenHub, CamState


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    add(LatestEvent(hub, cam) for cam in hub.cams.values())


class LatestEvent(ArgusSenCamEntity, SensorEntity):
    _attr_device_class = SensorDeviceClass.TIMESTAMP
    _attr_translation_key = "latest_event"
    _attr_icon = "mdi:cctv"

    def __init__(self, hub: ArgusSenHub, cam: CamState) -> None:
        super().__init__(hub, cam, "latest_event")

    @property
    def native_value(self) -> datetime | None:
        e = self.cam.latest
        return dt_util.parse_datetime(e["start"]) if e else None

    @property
    def extra_state_attributes(self) -> dict[str, Any] | None:
        e = self.cam.latest
        if not e:
            return None
        return {"event_id": e["id"], "labels": e.get("labels") or [], "ongoing": bool(e.get("ongoing")),
                "protected": bool(e.get("keep"))}

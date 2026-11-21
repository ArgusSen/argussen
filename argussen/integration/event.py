"""Event entities: one per camera, fired when a person, car, van, bicycle (or motion) is found."""

from __future__ import annotations

from typing import Any

from homeassistant.components.event import EventDeviceClass, EventEntity
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import ArgusSenConfigEntry
from .const import EVENT_TYPES, signal_update
from .entity import ArgusSenCamEntity
from .hub import ArgusSenHub, CamState


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    add(CamEvent(hub, cam) for cam in hub.cams.values())


class CamEvent(ArgusSenCamEntity, EventEntity):
    _attr_device_class = EventDeviceClass.MOTION
    _attr_event_types = list(EVENT_TYPES)
    _attr_translation_key = "detection"

    def __init__(self, hub: ArgusSenHub, cam: CamState) -> None:
        super().__init__(hub, cam, "detection")

    async def async_added_to_hass(self) -> None:
        await super().async_added_to_hass()
        self.async_on_remove(async_dispatcher_connect(
            self.hass, f"{signal_update(self.hub.entry.entry_id)}_event", self._detected))

    @callback
    def _detected(self, cam: str, new: list[str], e: dict[str, Any]) -> None:
        if cam != self.cam.id:
            return
        for label in new:
            if label in EVENT_TYPES:
                self._trigger_event(label, {"event_id": e["id"], "labels": e.get("labels") or [], "start": e.get("start")})
        self.async_write_ha_state()

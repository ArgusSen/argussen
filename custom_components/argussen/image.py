"""The picture of each camera's latest event."""

from __future__ import annotations

from homeassistant.components.image import ImageEntity
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback
from homeassistant.util import dt as dt_util

from . import ArgusSenConfigEntry
from .api import ArgusSenError
from .entity import ArgusSenCamEntity
from .hub import ArgusSenHub, CamState


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    add(LatestEventImage(hass, hub, cam) for cam in hub.cams.values())


class LatestEventImage(ArgusSenCamEntity, ImageEntity):
    _attr_content_type = "image/jpeg"
    _attr_translation_key = "latest_event_image"

    def __init__(self, hass: HomeAssistant, hub: ArgusSenHub, cam: CamState) -> None:
        ArgusSenCamEntity.__init__(self, hub, cam, "latest_event_image")
        ImageEntity.__init__(self, hass)
        self._shown: int | None = None
        self._set_time()

    def _set_time(self) -> None:
        e = self.cam.latest
        self._shown = e["id"] if e else None
        self._attr_image_last_updated = dt_util.parse_datetime(e["start"]) if e else None

    @callback
    def _update(self) -> None:
        e = self.cam.latest
        if (e["id"] if e else None) != self._shown or (e and e.get("ongoing")):
            self._set_time()
            self._cached_image = None
        self.async_write_ha_state()

    async def async_image(self) -> bytes | None:
        if not self.cam.latest:
            return None
        try:
            return await self.hub.client.thumbnail(self.cam.latest["id"])
        except ArgusSenError:
            return None

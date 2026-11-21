"""The base of all ArgusSen entities: devices and updates."""

from __future__ import annotations

from homeassistant.core import callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity import Entity

from .const import DOMAIN, signal_update
from .hub import ArgusSenHub, CamState


def hub_device(hub: ArgusSenHub) -> DeviceInfo:
    """The device for the ArgusSen server itself."""
    return DeviceInfo(
        identifiers={(DOMAIN, hub.instance or hub.entry.entry_id)},
        name="ArgusSen", manufacturer="ArgusSen", model="NVR", sw_version=hub.version,
        configuration_url=None if "://" not in hub.client.url or ":8099" in hub.client.url else hub.client.url,
    )


class ArgusSenEntity(Entity):
    """Updated when ArgusSen reports a change."""

    _attr_has_entity_name = True
    _attr_should_poll = False

    def __init__(self, hub: ArgusSenHub, key: str) -> None:
        self.hub = hub
        self._attr_unique_id = f"{hub.instance or hub.entry.entry_id}_{key}"
        self._attr_device_info = hub_device(hub)

    @property
    def available(self) -> bool:
        return self.hub.connected

    async def async_added_to_hass(self) -> None:
        self.async_on_remove(async_dispatcher_connect(self.hass, signal_update(self.hub.entry.entry_id), self._update))

    @callback
    def _update(self) -> None:
        self.async_write_ha_state()


class ArgusSenCamEntity(ArgusSenEntity):
    """An entity on a camera's device."""

    def __init__(self, hub: ArgusSenHub, cam: CamState, key: str) -> None:
        super().__init__(hub, f"{cam.id}_{key}")
        self.cam = cam
        base = hub.instance or hub.entry.entry_id
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, f"{base}_{cam.id}")}, name=cam.name, manufacturer="ArgusSen", model="Camera",
        )

    @property
    def available(self) -> bool:
        return self.hub.connected and self.cam.id in self.hub.cams

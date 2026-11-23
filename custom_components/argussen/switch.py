"""Switches: the home mode (no events from the chosen cameras while you are home – recording continues) and one per
camera, "Active" – the camera is switched on (recording) or off, as in ArgusSen's camera settings."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import ArgusSenConfigEntry
from .api import ArgusSenError
from .entity import ArgusSenCamEntity, ArgusSenEntity
from .hub import ArgusSenHub, CamState


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    if hub.admin:  # only administrators may see and set the home mode and switch cameras on and off
        add([HomeMode(hub)] + [CameraActive(hub, cam) for cam in hub.all_cams.values()])


class HomeMode(ArgusSenEntity, SwitchEntity):
    _attr_translation_key = "home_mode"
    _attr_icon = "mdi:home-account"

    def __init__(self, hub: ArgusSenHub) -> None:
        super().__init__(hub, "home_mode")

    @property
    def is_on(self) -> bool | None:
        return self.hub.home

    async def _set(self, on: bool) -> None:
        try:
            await self.hub.client.set_home(on)
        except ArgusSenError as err:
            raise HomeAssistantError(f"ArgusSen: {err}") from err
        self.hub.home = on
        self.async_write_ha_state()

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._set(True)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._set(False)


class CameraActive(ArgusSenCamEntity, SwitchEntity):
    """On = the camera records (and has its entities); off = switched off in ArgusSen."""

    _attr_translation_key = "camera_active"

    def __init__(self, hub: ArgusSenHub, cam: CamState) -> None:
        super().__init__(hub, cam, "active")

    @property
    def available(self) -> bool:
        return self.hub.connected  # also while the camera is switched off

    @property
    def is_on(self) -> bool:
        return self.hub.enabled.get(self.cam.id, False)

    @property
    def icon(self) -> str:
        return "mdi:cctv" if self.is_on else "mdi:cctv-off"

    async def _set(self, on: bool) -> None:
        try:
            await self.hub.client.set_camera_enabled(self.cam.id, on)
        except ArgusSenError as err:
            raise HomeAssistantError(f"ArgusSen: {err}") from err
        self.hub.enabled[self.cam.id] = on
        self.async_write_ha_state()  # ArgusSen then reports the change, and the integration is set up again

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._set(True)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._set(False)

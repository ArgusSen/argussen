"""Home mode: no events from the chosen cameras while you are home (recording continues)."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import ArgusSenConfigEntry
from .api import ArgusSenError
from .entity import ArgusSenEntity
from .hub import ArgusSenHub


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    if hub.admin:  # only administrators may see and set the home mode
        add([HomeMode(hub)])


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

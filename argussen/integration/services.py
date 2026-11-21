"""Services: protect an event (or remove the protection)."""

from __future__ import annotations

import voluptuous as vol

from homeassistant.core import HomeAssistant, ServiceCall
from homeassistant.exceptions import HomeAssistantError, ServiceValidationError
from homeassistant.helpers import config_validation as cv

from .api import ArgusSenError
from .const import DOMAIN

SCHEMA = vol.Schema({
    vol.Required("event_id"): cv.positive_int,
    vol.Optional("protect", default=True): cv.boolean,
    vol.Optional("config_entry_id"): cv.string,
})


def async_setup_services(hass: HomeAssistant) -> None:
    async def protect_event(call: ServiceCall) -> None:
        entries = [e for e in hass.config_entries.async_entries(DOMAIN) if hasattr(e, "runtime_data")]
        if want := call.data.get("config_entry_id"):
            entries = [e for e in entries if e.entry_id == want]
        if not entries:
            raise ServiceValidationError(translation_domain=DOMAIN, translation_key="no_server")
        try:
            await entries[0].runtime_data.client.protect_event(call.data["event_id"], call.data["protect"])
        except ArgusSenError as err:
            raise HomeAssistantError(f"ArgusSen: {err}") from err

    hass.services.async_register(DOMAIN, "protect_event", protect_event, schema=SCHEMA)

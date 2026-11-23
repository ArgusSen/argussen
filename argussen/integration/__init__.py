"""ArgusSen – NVR for Home Assistant: cameras, live video, events and recordings."""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed, ConfigEntryNotReady
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.typing import ConfigType

from .api import ArgusSenAuthError, ArgusSenClient, ArgusSenError
from .const import CONF_TOKEN, CONF_URL, DOMAIN
from .hub import ArgusSenHub
from .restart import async_remember_files
from .services import async_setup_services
from .views import ArgusSenClipView

PLATFORMS = [
    Platform.BINARY_SENSOR, Platform.CAMERA, Platform.EVENT, Platform.IMAGE, Platform.SENSOR, Platform.SWITCH,
]

CONFIG_SCHEMA = cv.config_entry_only_config_schema(DOMAIN)

type ArgusSenConfigEntry = ConfigEntry[ArgusSenHub]


async def async_setup(hass: HomeAssistant, config: ConfigType) -> bool:
    """Services and the clip view (shared by all ArgusSen servers)."""
    async_setup_services(hass)
    hass.http.register_view(ArgusSenClipView(hass))
    await async_remember_files(hass)
    return True


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry) -> bool:
    """Connect to one ArgusSen server."""
    client = ArgusSenClient(async_get_clientsession(hass), entry.data[CONF_URL], entry.data[CONF_TOKEN])
    try:
        me = await client.me()
    except ArgusSenAuthError as err:
        raise ConfigEntryAuthFailed from err
    except ArgusSenError as err:
        raise ConfigEntryNotReady(f"ArgusSen is not answering: {err}") from err
    hub = ArgusSenHub(hass, entry, client, me)
    try:
        await hub.async_setup()
    except ArgusSenAuthError as err:
        raise ConfigEntryAuthFailed from err
    except ArgusSenError as err:
        raise ConfigEntryNotReady(f"ArgusSen is not answering: {err}") from err
    entry.runtime_data = hub
    entry.async_on_unload(entry.add_update_listener(_options_changed))
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def _options_changed(hass: HomeAssistant, entry: ArgusSenConfigEntry) -> None:
    """The options changed (e.g. SD/HD): set the cameras up again."""
    await hass.config_entries.async_reload(entry.entry_id)


async def async_unload_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry) -> bool:
    """Disconnect."""
    await entry.runtime_data.async_stop()
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)

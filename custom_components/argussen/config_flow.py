"""Setting up ArgusSen: discovered automatically (the Home Assistant app), or by address and login (a separate server)."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigEntry, ConfigFlow, ConfigFlowResult, OptionsFlow
from homeassistant.core import callback
from homeassistant.helpers.selector import SelectSelector, SelectSelectorConfig, SelectSelectorMode
from homeassistant.const import CONF_PASSWORD, CONF_USERNAME
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.service_info.hassio import HassioServiceInfo

from .api import ArgusSenAuthError, ArgusSenClient, ArgusSenError
from .const import CONF_STREAM_QUALITY, CONF_TOKEN, CONF_URL, DOMAIN


def _clean_url(url: str) -> str:
    url = url.strip().rstrip("/")
    return url if "://" in url else "http://" + url


class ArgusSenConfigFlow(ConfigFlow, domain=DOMAIN):
    """Set up an ArgusSen server."""

    VERSION = 1

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> OptionsFlow:
        return ArgusSenOptionsFlow()

    def __init__(self) -> None:
        self._discovered: dict[str, Any] = {}

    async def _login(self, url: str, username: str, password: str) -> tuple[str, dict[str, Any]]:
        session = async_get_clientsession(self.hass)
        token = await ArgusSenClient.login(session, url, username, password)
        return token, await ArgusSenClient(session, url, token).me()

    async def async_step_user(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        """A separate ArgusSen server: its address and an ArgusSen user."""
        errors: dict[str, str] = {}
        if user_input is not None:
            url = _clean_url(user_input[CONF_URL])
            try:
                token, me = await self._login(url, user_input[CONF_USERNAME], user_input[CONF_PASSWORD])
            except ArgusSenAuthError:
                errors["base"] = "invalid_auth"
            except ArgusSenError:
                errors["base"] = "cannot_connect"
            else:
                await self.async_set_unique_id(me.get("instance") or url)
                self._abort_if_unique_id_configured(updates={CONF_URL: url, CONF_TOKEN: token})
                return self.async_create_entry(title="ArgusSen", data={CONF_URL: url, CONF_TOKEN: token})
        schema = vol.Schema({
            vol.Required(CONF_URL, default=(user_input or {}).get(CONF_URL, "http://")): str,
            vol.Required(CONF_USERNAME, default=(user_input or {}).get(CONF_USERNAME, "")): str,
            vol.Required(CONF_PASSWORD): str,
        })
        return self.async_show_form(step_id="user", data_schema=schema, errors=errors)

    async def async_step_hassio(self, discovery_info: HassioServiceInfo) -> ConfigFlowResult:
        """The ArgusSen app announced itself (address and token)."""
        cfg = discovery_info.config
        url, token = f"http://{cfg['host']}:{cfg['port']}", cfg["token"]
        try:
            me = await ArgusSenClient(async_get_clientsession(self.hass), url, token).me()
        except ArgusSenError:
            return self.async_abort(reason="cannot_connect")
        await self.async_set_unique_id(me.get("instance") or discovery_info.uuid)
        # already set up: the app may have a new address or token after an update – keep it current
        self._abort_if_unique_id_configured(updates={CONF_URL: url, CONF_TOKEN: token})
        self._discovered = {CONF_URL: url, CONF_TOKEN: token}
        return await self.async_step_hassio_confirm()

    async def async_step_hassio_confirm(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        if user_input is not None:
            return self.async_create_entry(title="ArgusSen", data=self._discovered)
        self._set_confirm_only()
        return self.async_show_form(step_id="hassio_confirm")

    async def async_step_reauth(self, entry_data: Mapping[str, Any]) -> ConfigFlowResult:
        """ArgusSen no longer accepts the token (e.g. the user was logged out)."""
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        entry = self._get_reauth_entry()
        if user_input is not None:
            try:
                token, _ = await self._login(entry.data[CONF_URL], user_input[CONF_USERNAME], user_input[CONF_PASSWORD])
            except ArgusSenAuthError:
                errors["base"] = "invalid_auth"
            except ArgusSenError:
                errors["base"] = "cannot_connect"
            else:
                return self.async_update_reload_and_abort(entry, data_updates={CONF_TOKEN: token})
        schema = vol.Schema({vol.Required(CONF_USERNAME): str, vol.Required(CONF_PASSWORD): str})
        return self.async_show_form(step_id="reauth_confirm", data_schema=schema, errors=errors)


class ArgusSenOptionsFlow(OptionsFlow):
    """Options: the quality of the live video in Home Assistant's camera cards."""

    async def async_step_init(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        if user_input is not None:
            return self.async_create_entry(data=user_input)
        schema = vol.Schema({
            vol.Required(CONF_STREAM_QUALITY, default=self.config_entry.options.get(CONF_STREAM_QUALITY, "sd")):
                SelectSelector(SelectSelectorConfig(options=["sd", "hd"], translation_key=CONF_STREAM_QUALITY,
                                                    mode=SelectSelectorMode.LIST)),
        })
        return self.async_show_form(step_id="init", data_schema=schema)

"""Device triggers for a camera: "Person detected", "Car detected" … and, per zone, "Person came into Driveway".

They are built on the bus event argussen_event, so automations can be made from the camera's device without knowing
the entity names: Automation → Trigger → Device → the camera.
"""

from __future__ import annotations

from typing import Any

import voluptuous as vol

from homeassistant.components.device_automation import DEVICE_TRIGGER_BASE_SCHEMA
from homeassistant.const import CONF_DEVICE_ID, CONF_DOMAIN, CONF_PLATFORM, CONF_TYPE
from homeassistant.core import CALLBACK_TYPE, Event, HassJob, HomeAssistant, callback
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.trigger import TriggerActionType, TriggerInfo
from homeassistant.helpers.typing import ConfigType

from .const import BUS_EVENT, DOMAIN, EVENT_TYPES

CONF_SUBTYPE = "subtype"
CONF_ZONE = "zone_id"
ZONE_TYPES = tuple(f"zone_{t}" for t in EVENT_TYPES)  # zone_person … – the subtype is the zone's name

TRIGGER_SCHEMA = DEVICE_TRIGGER_BASE_SCHEMA.extend({
    vol.Required(CONF_TYPE): vol.In((*EVENT_TYPES, *ZONE_TYPES)),
    vol.Optional(CONF_SUBTYPE): str,
    vol.Optional(CONF_ZONE): str,
})


def _camera(hass: HomeAssistant, device_id: str) -> tuple[str, str, Any] | None:
    """The config entry id, the camera id and the hub of a camera's device (None for other devices)."""
    device = dr.async_get(hass).async_get(device_id)
    if device is None:
        return None
    for entry_id in device.config_entries:
        entry = hass.config_entries.async_get_entry(entry_id)
        if entry is None or entry.domain != DOMAIN or not hasattr(entry, "runtime_data"):
            continue
        hub = entry.runtime_data
        base = hub.instance or entry.entry_id
        for domain, ident in device.identifiers:
            if domain == DOMAIN and ident.startswith(base + "_") and ident[len(base) + 1:] in hub.all_cams | hub.cams:
                return entry.entry_id, ident[len(base) + 1:], hub
    return None


async def async_get_triggers(hass: HomeAssistant, device_id: str) -> list[dict[str, Any]]:
    """The triggers of a camera's device."""
    found = _camera(hass, device_id)
    if found is None:
        return []
    _, cam_id, hub = found
    base = {CONF_PLATFORM: "device", CONF_DOMAIN: DOMAIN, CONF_DEVICE_ID: device_id}
    triggers = [{**base, CONF_TYPE: t} for t in EVENT_TYPES]
    cam = hub.cams.get(cam_id)
    for zone_id, name in (cam.zones if cam else {}).items():
        triggers += [{**base, CONF_TYPE: f"zone_{t}", CONF_SUBTYPE: name, CONF_ZONE: zone_id} for t in EVENT_TYPES]
    return triggers


async def async_attach_trigger(hass: HomeAssistant, config: ConfigType, action: TriggerActionType,
                               trigger_info: TriggerInfo) -> CALLBACK_TYPE:
    """Listen to argussen_event for the camera (and zone) of the trigger."""
    found = _camera(hass, config[CONF_DEVICE_ID])
    entry_id, cam_id = (found[0], found[1]) if found else (None, None)
    typ: str = config[CONF_TYPE]
    zone = config.get(CONF_ZONE)
    label = typ.removeprefix("zone_")
    job = HassJob(action)
    trigger_data = trigger_info["trigger_data"]

    @callback
    def _event(event: Event) -> None:
        d = event.data
        if d.get("entry_id") != entry_id or d.get("camera") != cam_id:
            return
        if zone:
            if label not in (d.get("zone_new") or {}).get(zone, []):
                return
        elif label not in (d.get("new_labels") or []):
            return
        hass.async_run_hass_job(job, {"trigger": {
            **trigger_data, CONF_PLATFORM: "device", CONF_DOMAIN: DOMAIN, CONF_DEVICE_ID: config[CONF_DEVICE_ID],
            CONF_TYPE: typ, "event": event, "description": f"ArgusSen {typ}",
        }})

    return hass.bus.async_listen(BUS_EVENT, _event)

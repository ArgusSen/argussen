"""Motion, connection, the objects in ongoing events, and "storage almost full"."""

from __future__ import annotations

from homeassistant.components.binary_sensor import BinarySensorDeviceClass, BinarySensorEntity
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import ArgusSenConfigEntry
from .const import OBJECTS
from .entity import ArgusSenCamEntity, ArgusSenEntity
from .hub import ArgusSenHub, CamState

ICONS = {"person": "mdi:walk", "car": "mdi:car", "van": "mdi:van-utility", "bicycle": "mdi:bicycle"}


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    entities: list[BinarySensorEntity] = [StorageLow(hub)]
    for cam in hub.cams.values():
        entities += [Motion(hub, cam), Connection(hub, cam)]
        entities += [ObjectSeen(hub, cam, o) for o in OBJECTS]
    add(entities)


class Motion(ArgusSenCamEntity, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.MOTION
    _attr_translation_key = "motion"

    def __init__(self, hub: ArgusSenHub, cam: CamState) -> None:
        super().__init__(hub, cam, "motion")

    @property
    def is_on(self) -> bool:
        return self.cam.motion


class Connection(ArgusSenCamEntity, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_translation_key = "connection"

    def __init__(self, hub: ArgusSenHub, cam: CamState) -> None:
        super().__init__(hub, cam, "connection")

    @property
    def is_on(self) -> bool:
        return self.cam.online


class ObjectSeen(ArgusSenCamEntity, BinarySensorEntity):
    """On while an event with the object is going on."""

    def __init__(self, hub: ArgusSenHub, cam: CamState, obj: str) -> None:
        super().__init__(hub, cam, obj)
        self.obj = obj
        self._attr_translation_key = obj
        self._attr_icon = ICONS[obj]

    @property
    def is_on(self) -> bool:
        return self.obj in self.cam.objects


class StorageLow(ArgusSenEntity, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.PROBLEM
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_translation_key = "storage_low"

    def __init__(self, hub: ArgusSenHub) -> None:
        super().__init__(hub, "storage_low")

    @property
    def is_on(self) -> bool:
        return self.hub.storage_low

"""Cameras: a still image and live video (HLS from ArgusSen, with the same connection as the recording)."""

from __future__ import annotations

from homeassistant.components.camera import Camera, CameraEntityFeature
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import ArgusSenConfigEntry
from .api import ArgusSenError
from .const import CONF_STREAM_QUALITY
from .entity import ArgusSenCamEntity
from .hub import ArgusSenHub, CamState


async def async_setup_entry(hass: HomeAssistant, entry: ArgusSenConfigEntry, add: AddConfigEntryEntitiesCallback) -> None:
    hub = entry.runtime_data
    add(ArgusSenCamera(hub, cam) for cam in hub.cams.values())


class ArgusSenCamera(ArgusSenCamEntity, Camera):
    """One camera."""

    _attr_name = None  # the device's name
    _attr_supported_features = CameraEntityFeature.STREAM

    def __init__(self, hub: ArgusSenHub, cam: CamState) -> None:
        ArgusSenCamEntity.__init__(self, hub, cam, "camera")
        Camera.__init__(self)

    @property
    def available(self) -> bool:
        return super().available and self.cam.online

    @property
    def is_recording(self) -> bool:
        return self.cam.online

    @property
    def motion_detection_enabled(self) -> bool:
        return True

    async def async_camera_image(self, width: int | None = None, height: int | None = None) -> bytes | None:
        try:
            return await self.hub.client.snapshot(self.cam.id)
        except ArgusSenError:
            return None

    async def stream_source(self) -> str | None:
        # SD by default: lighter for camera cards, phones and mobile data (HD is in ArgusSen itself, or an option)
        hd = self.hub.entry.options.get(CONF_STREAM_QUALITY, "sd") == "hd"
        return self.hub.client.live_url(self.cam.id, "main" if hd else "sub")

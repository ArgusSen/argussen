"""Fixtures: a made-up ArgusSen server."""

import asyncio
from unittest.mock import patch

import pytest

pytest_plugins = "pytest_homeassistant_custom_component"

ME = {"user": {"id": 1, "name": "admin", "admin": True, "cams": ["*"]}, "version": "0.10.0", "instance": "abc123"}
CAMS = [{"id": "driveway", "name": "Driveway", "online": True, "motion": False,
         "zones": [{"id": "z1", "name": "Path", "kind": "zone", "points": [[0, 0], [1, 0], [1, 1]]},
                   {"id": "i1", "name": "", "kind": "ignore", "points": [[0, 0], [1, 0], [1, 1]]}]},
        {"id": "street", "name": "Street", "online": True, "motion": False}]
ADMIN_CAMS = [{"id": "driveway", "name": "Driveway", "enabled": True}, {"id": "street", "name": "Street", "enabled": True},
              {"id": "garage", "name": "Garage", "enabled": False}]
EVENTS = [{"id": 7, "cam": "driveway", "start": "2026-11-20T14:31:44+01:00", "end": "2026-11-20T14:32:02+01:00",
           "labels": ["person"], "score": 0.91, "boxes": [], "keep": False}]


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    yield


class FakeStream:
    """Messages for ArgusSenClient.stream: put (kind, data) in queue."""

    def __init__(self):
        self.queue: asyncio.Queue = asyncio.Queue()

    async def __call__(self, *args):
        yield "state", {"cams": CAMS, "home": False, "storage_low": False}
        while True:
            yield await self.queue.get()


@pytest.fixture
def fake_server():
    stream = FakeStream()
    with patch("custom_components.argussen.api.ArgusSenClient.me", return_value=ME), \
         patch("custom_components.argussen.api.ArgusSenClient.cameras", return_value=CAMS), \
         patch("custom_components.argussen.api.ArgusSenClient.events", return_value=EVENTS), \
         patch("custom_components.argussen.api.ArgusSenClient.admin_cameras", return_value=ADMIN_CAMS), \
         patch("custom_components.argussen.api.ArgusSenClient.set_camera_enabled", return_value=None) as enable, \
         patch("custom_components.argussen.api.ArgusSenClient.stream", new=lambda self: stream()), \
         patch("custom_components.argussen.api.ArgusSenClient.login", return_value="tok"):
        stream.enable = enable
        yield stream

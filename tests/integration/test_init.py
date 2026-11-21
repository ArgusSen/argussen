"""Setting up ArgusSen and its entities."""

from homeassistant import config_entries
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers.service_info.hassio import HassioServiceInfo
from pytest_homeassistant_custom_component.common import MockConfigEntry

from custom_components.argussen.const import BUS_EVENT, DOMAIN


async def test_user_flow(hass: HomeAssistant, fake_server) -> None:
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_USER})
    assert result["type"] is FlowResultType.FORM
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {"url": "192.168.1.20:8080", "username": "admin", "password": "secret12"})
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["data"] == {"url": "http://192.168.1.20:8080", "token": "tok"}
    assert result["result"].unique_id == "abc123"


async def test_hassio_discovery(hass: HomeAssistant, fake_server) -> None:
    info = HassioServiceInfo(config={"host": "1a2b-argussen", "port": 8099, "token": "t1"}, name="ArgusSen",
                             slug="1a2b_argussen", uuid="u1")
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_HASSIO}, data=info)
    assert result["type"] is FlowResultType.FORM and result["step_id"] == "hassio_confirm"
    result = await hass.config_entries.flow.async_configure(result["flow_id"], {})
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["data"]["url"] == "http://1a2b-argussen:8099"
    await hass.async_block_till_done()
    # the app announces itself again with a new token: the entry is updated, no new one
    info2 = HassioServiceInfo(config={"host": "1a2b-argussen", "port": 8099, "token": "t2"}, name="ArgusSen",
                              slug="1a2b_argussen", uuid="u1")
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_HASSIO}, data=info2)
    assert result["type"] is FlowResultType.ABORT and result["reason"] == "already_configured"
    assert hass.config_entries.async_entries(DOMAIN)[0].data["token"] == "t2"


async def test_entities_and_events(hass: HomeAssistant, fake_server) -> None:
    entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()

    assert hass.states.get("camera.driveway") is not None
    assert hass.states.get("binary_sensor.driveway_motion").state == "off"
    assert hass.states.get("binary_sensor.driveway_person").state == "off"
    assert hass.states.get("switch.argussen_home_mode").state == "off"
    assert hass.states.get("sensor.driveway_latest_event").attributes["event_id"] == 7

    fired = []
    hass.bus.async_listen(BUS_EVENT, lambda e: fired.append(e.data))
    # motion, then an event with a person and a car that goes on, then ends
    await fake_server.queue.put(("cam", {"id": "street", "name": "Street", "online": True, "motion": True}))
    await fake_server.queue.put(("event", {"id": 8, "cam": "street", "start": "2026-11-20T15:00:00+01:00",
                                           "end": "0001-01-01T00:00:00Z", "labels": ["car", "person"], "score": 0.9,
                                           "boxes": [], "keep": False, "ongoing": True}))
    await hass.async_block_till_done()
    assert hass.states.get("binary_sensor.street_motion").state == "on"
    assert hass.states.get("binary_sensor.street_person").state == "on"
    assert hass.states.get("binary_sensor.street_car").state == "on"
    assert hass.states.get("event.street_detection").attributes["event_type"] in ("car", "person")
    assert fired and fired[-1]["new_labels"] == ["car", "person"] and fired[-1]["ongoing"]

    await fake_server.queue.put(("event", {"id": 8, "cam": "street", "start": "2026-11-20T15:00:00+01:00",
                                           "end": "2026-11-20T15:00:20+01:00", "labels": ["car", "person"], "score": 0.9,
                                           "boxes": [], "keep": False, "ongoing": False}))
    await hass.async_block_till_done()
    assert hass.states.get("binary_sensor.street_person").state == "off"
    assert hass.states.get("sensor.street_latest_event").attributes["event_id"] == 8
    assert fired[-1]["ongoing"] is False and fired[-1]["new_labels"] == []

    assert await hass.config_entries.async_unload(entry.entry_id)


async def test_stream_quality(hass: HomeAssistant, fake_server) -> None:
    entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    cam = hass.data["camera"].get_entity("camera.driveway")
    assert (await cam.stream_source()).endswith("/api/live/driveway/sub/index.m3u8?token=t")  # SD by default
    result = await hass.config_entries.options.async_init(entry.entry_id)
    result = await hass.config_entries.options.async_configure(result["flow_id"], {"stream_quality": "hd"})
    assert result["type"] is FlowResultType.CREATE_ENTRY
    await hass.async_block_till_done()
    cam = hass.data["camera"].get_entity("camera.driveway")
    assert "/api/live/driveway/main/" in await cam.stream_source()
    assert await hass.config_entries.async_unload(entry.entry_id)

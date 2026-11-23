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


async def test_camera_active_switch(hass: HomeAssistant, fake_server) -> None:
    """A switch per camera ("Active"), also for cameras that are switched off – for automations."""
    entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    assert hass.states.get("switch.driveway_active").state == "on"
    garage = hass.states.get("switch.garage_active")
    assert garage is not None and garage.state == "off"  # switched off: the switch is there, the camera is not
    assert hass.states.get("camera.garage") is None
    await hass.services.async_call("switch", "turn_on", {"entity_id": "switch.garage_active"}, blocking=True)
    fake_server.enable.assert_called_once_with("garage", True)
    assert hass.states.get("switch.garage_active").state == "on"
    # ArgusSen reports that the cameras changed: the integration is set up again
    await fake_server.queue.put(("cameras", {"changed": True}))
    await hass.async_block_till_done()
    assert hass.config_entries.async_get_entry(entry.entry_id).state is config_entries.ConfigEntryState.LOADED
    assert await hass.config_entries.async_unload(entry.entry_id)


async def test_restart_required_repair(hass: HomeAssistant, fake_server) -> None:
    """The app put new files on disk: "Restart required" under Repairs, and its button restarts Home Assistant."""
    from homeassistant.helpers import issue_registry as ir
    from custom_components.argussen import restart

    marker = restart.MARKER
    old = marker.read_text() if marker.exists() else None
    try:
        marker.write_text("aaaa\n")
        entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
        entry.add_to_hass(hass)
        assert await hass.config_entries.async_setup(entry.entry_id)
        await hass.async_block_till_done()
        await restart.async_check_files(hass)  # nothing changed
        assert ir.async_get(hass).async_get_issue(DOMAIN, restart.ISSUE) is None
        from homeassistant.components import persistent_notification
        persistent_notification.async_create(hass, "restart", "ArgusSen", restart.NOTIFICATION)  # the app's notification
        marker.write_text("bbbb\n")  # the app installed a new version
        await restart.async_check_files(hass)
        assert ir.async_get(hass).async_get_issue(DOMAIN, restart.ISSUE) is not None
        await hass.async_block_till_done()
        assert restart.NOTIFICATION not in persistent_notification._async_get_or_create_notifications(hass)

        calls = []
        hass.services.async_register("homeassistant", "restart", lambda call: calls.append(call))
        from custom_components.argussen.repairs import async_create_fix_flow
        flow = await async_create_fix_flow(hass, restart.ISSUE, None)
        flow.hass = hass
        result = await flow.async_step_init()
        assert result["step_id"] == "confirm"
        await flow.async_step_confirm({})
        await hass.async_block_till_done()
        assert calls, "Home Assistant was not restarted"
        assert await hass.config_entries.async_unload(entry.entry_id)
    finally:
        if old is None:
            marker.unlink(missing_ok=True)
        else:
            marker.write_text(old)


async def test_zones(hass: HomeAssistant, fake_server) -> None:
    """A zone: a binary sensor (on while something is in it) and an event entity; areas to ignore get none."""
    entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    assert hass.states.get("binary_sensor.driveway_path").state == "off"
    assert hass.states.get("event.driveway_path_detection") is not None
    assert not [s for s in hass.states.async_entity_ids() if "i1" in s or "ignore" in s]
    fired = []
    hass.bus.async_listen(BUS_EVENT, lambda e: fired.append(e.data))
    ev = {"id": 9, "cam": "driveway", "start": "2026-11-23T10:00:00+01:00", "end": "0001-01-01T00:00:00Z",
          "labels": ["person"], "score": 0.9, "boxes": [], "keep": False, "ongoing": True, "zones": ["z1"]}
    await fake_server.queue.put(("event", ev))
    await hass.async_block_till_done()
    on = hass.states.get("binary_sensor.driveway_path")
    assert on.state == "on" and on.attributes["objects"] == ["person"]
    assert hass.states.get("event.driveway_path_detection").attributes["event_type"] == "person"
    assert fired[-1]["zones"] == ["z1"] and fired[-1]["zone_names"] == ["Path"]
    await fake_server.queue.put(("event", dict(ev, ongoing=False, end="2026-11-23T10:00:30+01:00")))
    await hass.async_block_till_done()
    assert hass.states.get("binary_sensor.driveway_path").state == "off"
    assert await hass.config_entries.async_unload(entry.entry_id)


async def test_device_triggers(hass: HomeAssistant, fake_server) -> None:
    """Automation → Trigger → Device → a camera: "Person detected", and per zone "Person came into Path"."""
    from homeassistant.components.device_automation import DeviceAutomationType
    from homeassistant.helpers import device_registry as dr
    from homeassistant.setup import async_setup_component
    from pytest_homeassistant_custom_component.common import async_get_device_automations, async_mock_service

    entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    device = dr.async_get(hass).async_get_device(identifiers={(DOMAIN, "abc123_driveway")})
    triggers = await async_get_device_automations(hass, DeviceAutomationType.TRIGGER, device.id)
    types = {(t["type"], t.get("subtype")) for t in triggers if t["domain"] == DOMAIN}
    assert ("person", None) in types and ("motion", None) in types and ("zone_person", "Path") in types
    hub_device = dr.async_get(hass).async_get_device(identifiers={(DOMAIN, "abc123")})
    assert not [t for t in await async_get_device_automations(hass, DeviceAutomationType.TRIGGER, hub_device.id) if t["domain"] == DOMAIN]

    calls = async_mock_service(hass, "test", "automation")
    assert await async_setup_component(hass, "automation", {"automation": [
        {"trigger": {"platform": "device", "domain": DOMAIN, "device_id": device.id, "type": "person"},
         "action": {"service": "test.automation", "data": {"what": "person"}}},
        {"trigger": {"platform": "device", "domain": DOMAIN, "device_id": device.id, "type": "zone_person", "subtype": "Path", "zone_id": "z1"},
         "action": {"service": "test.automation", "data": {"what": "zone"}}},
        {"trigger": {"platform": "device", "domain": DOMAIN, "device_id": device.id, "type": "car"},
         "action": {"service": "test.automation", "data": {"what": "car"}}},
    ]})
    await hass.async_block_till_done()
    await fake_server.queue.put(("event", {"id": 11, "cam": "driveway", "start": "2026-11-23T10:00:00+01:00",
                                           "end": "0001-01-01T00:00:00Z", "labels": ["person"], "score": 0.9, "boxes": [],
                                           "keep": False, "ongoing": True, "zones": ["z1"]}))
    await hass.async_block_till_done()
    assert sorted(c.data["what"] for c in calls) == ["person", "zone"]  # no car
    assert await hass.config_entries.async_unload(entry.entry_id)


async def test_blueprint(hass: HomeAssistant, fake_server) -> None:
    """The blueprint "notify when something is detected": the right camera, objects and zone (the phone is replaced)."""
    import pathlib
    from homeassistant.helpers import device_registry as dr
    from homeassistant.setup import async_setup_component
    from pytest_homeassistant_custom_component.common import async_mock_service

    src = None
    for p in (pathlib.Path(__file__).parents[1] / "argus/blueprints/notify.yaml",
              pathlib.Path(__file__).parents[2] / "argussen/blueprints/notify.yaml",
              pathlib.Path(__file__).parents[1].parent / "argus/blueprints/notify.yaml"):
        if p.exists():
            src = p.read_text()
    assert src, "blueprint not found"
    # the phone: a test service instead of the mobile app's device action
    start = src.index("  - device_id: !input phone")
    end = src.index("  - delay:")
    src = src[:start] + """  - action: test.automation
    data:
      title: "{{ trigger.event.data.camera_name }}"
      message: "{{ found }}"
      image: "{{ '/api/camera_proxy/' ~ camera_entity if camera_entity else '' }}"
""" + src[end:]
    src = src.replace("    phone:\n      name:", "    phone_unused:\n      name:")
    bp = pathlib.Path(hass.config.path("blueprints/automation/argussen/notify.yaml"))
    bp.parent.mkdir(parents=True, exist_ok=True)
    await hass.async_add_executor_job(bp.write_text, src)

    entry = MockConfigEntry(domain=DOMAIN, data={"url": "http://x:8099", "token": "t"}, unique_id="abc123")
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    device = dr.async_get(hass).async_get_device(identifiers={(DOMAIN, "abc123_driveway")})
    calls = async_mock_service(hass, "test", "automation")
    assert await async_setup_component(hass, "automation", {"automation": [
        {"use_blueprint": {"path": "argussen/notify.yaml", "input": {
            "camera": device.id, "objects": ["person", "car"], "zone": "", "phone_unused": "x", "pause": 0}}},
        {"use_blueprint": {"path": "argussen/notify.yaml", "input": {
            "camera": device.id, "objects": ["person"], "zone": "binary_sensor.driveway_path", "phone_unused": "x", "pause": 0}}},
    ]})
    await hass.async_block_till_done()

    def ev(i, cam, labels, zones):
        return ("event", {"id": i, "cam": cam, "start": "2026-11-23T10:00:00+01:00", "end": "0001-01-01T00:00:00Z",
                          "labels": labels, "score": 0.9, "boxes": [], "keep": False, "ongoing": True, "zones": zones})
    await fake_server.queue.put(ev(21, "street", ["person"], []))     # another camera: nothing
    await fake_server.queue.put(ev(22, "driveway", ["bicycle"], []))  # not chosen: nothing
    await fake_server.queue.put(ev(23, "driveway", ["person"], []))   # person, not in the zone: only the first
    await fake_server.queue.put(ev(24, "driveway", ["car"], ["z1"]))  # car in the zone: only the first (the zone counts persons)
    await fake_server.queue.put(ev(25, "driveway", ["person"], ["z1"]))  # person in the zone: both
    import asyncio
    for _ in range(20):  # the stream is read in the background
        await hass.async_block_till_done()
        if len(calls) >= 4:
            break
        await asyncio.sleep(0.05)
    got = [c.data["message"] for c in calls]
    assert got.count("Person") == 3 and got.count("Car") == 1 and len(got) == 4, got
    assert all(c.data["title"] == "Driveway" for c in calls)
    assert await hass.config_entries.async_unload(entry.entry_id)

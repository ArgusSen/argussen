"""ArgusSen in Home Assistant's media browser: servers → cameras → events (video clips)."""

from __future__ import annotations

from datetime import timedelta

from homeassistant.components.http.auth import async_sign_path
from homeassistant.components.media_player import MediaClass, MediaType
from homeassistant.components.media_source import BrowseMediaSource, MediaSource, MediaSourceItem, PlayMedia, Unresolvable
from homeassistant.core import HomeAssistant
from homeassistant.util import dt as dt_util

from .api import ArgusSenError
from .const import DOMAIN


async def async_get_media_source(hass: HomeAssistant) -> MediaSource:
    return ArgusSenMediaSource(hass)


class ArgusSenMediaSource(MediaSource):
    name = "ArgusSen"

    def __init__(self, hass: HomeAssistant) -> None:
        super().__init__(DOMAIN)
        self.hass = hass

    def _entries(self):
        return [e for e in self.hass.config_entries.async_entries(DOMAIN) if hasattr(e, "runtime_data")]

    async def async_resolve_media(self, item: MediaSourceItem) -> PlayMedia:
        parts = item.identifier.split("/")
        if len(parts) != 3 or not parts[2].isdigit():
            raise Unresolvable("Not an ArgusSen event")
        path = async_sign_path(self.hass, f"/api/argussen/{parts[0]}/{parts[2]}/clip.mp4", timedelta(minutes=30))
        return PlayMedia(path, "video/mp4")

    async def async_browse_media(self, item: MediaSourceItem) -> BrowseMediaSource:
        parts = [p for p in (item.identifier or "").split("/") if p]
        entries = self._entries()
        if not parts and len(entries) == 1:
            parts = [entries[0].entry_id]
        if not parts:
            return self._dir("", "ArgusSen", [self._dir(e.entry_id, e.title, []) for e in entries])
        entry = self.hass.config_entries.async_get_entry(parts[0])
        if entry is None or not hasattr(entry, "runtime_data"):
            raise Unresolvable("Unknown ArgusSen server")
        hub = entry.runtime_data
        if len(parts) == 1:
            return self._dir(entry.entry_id, "ArgusSen",
                             [self._dir(f"{entry.entry_id}/{c.id}", c.name, []) for c in hub.cams.values()])
        cam = hub.cams.get(parts[1])
        if cam is None:
            raise Unresolvable("Unknown camera")
        try:
            events = await hub.client.events(limit=100, cam=cam.id)
        except ArgusSenError as err:
            raise Unresolvable(str(err)) from err
        children = []
        for e in events:
            start = dt_util.as_local(dt_util.parse_datetime(e["start"]))
            title = f"{start:%Y-%m-%d %H:%M} – {', '.join(e.get('labels') or [])}"
            thumb = async_sign_path(self.hass, f"/api/argussen/{entry.entry_id}/{e['id']}/thumb.jpg", timedelta(hours=1))
            children.append(BrowseMediaSource(
                domain=DOMAIN, identifier=f"{entry.entry_id}/{cam.id}/{e['id']}", media_class=MediaClass.VIDEO,
                media_content_type="video/mp4", title=title, can_play=True, can_expand=False, thumbnail=thumb))
        return BrowseMediaSource(
            domain=DOMAIN, identifier=f"{entry.entry_id}/{cam.id}", media_class=MediaClass.DIRECTORY,
            media_content_type=MediaType.VIDEO, title=cam.name, can_play=False, can_expand=True,
            children=children, children_media_class=MediaClass.VIDEO)

    @staticmethod
    def _dir(identifier: str, title: str, children: list[BrowseMediaSource]) -> BrowseMediaSource:
        return BrowseMediaSource(
            domain=DOMAIN, identifier=identifier or None, media_class=MediaClass.DIRECTORY,
            media_content_type=MediaType.VIDEO, title=title, can_play=False, can_expand=True,
            children=children, children_media_class=MediaClass.DIRECTORY)

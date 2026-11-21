"""Event clips and pictures for Home Assistant's media browser (ArgusSen needs a token, the browser has none)."""

from __future__ import annotations

from aiohttp import web

from homeassistant.components.http import HomeAssistantView
from homeassistant.core import HomeAssistant

from .const import DOMAIN


class ArgusSenClipView(HomeAssistantView):
    """/api/argussen/{entry_id}/{event_id}/clip.mp4 and …/thumb.jpg – only with Home Assistant login or a signed path."""

    url = "/api/argussen/{entry_id}/{event_id}/{kind}"
    name = "api:argussen:media"
    requires_auth = True

    def __init__(self, hass: HomeAssistant) -> None:
        self.hass = hass

    async def get(self, request: web.Request, entry_id: str, event_id: str, kind: str) -> web.StreamResponse:
        entry = self.hass.config_entries.async_get_entry(entry_id)
        if entry is None or entry.domain != DOMAIN or not hasattr(entry, "runtime_data") or not event_id.isdigit():
            raise web.HTTPNotFound
        client = entry.runtime_data.client
        if kind == "thumb.jpg":
            return web.Response(body=await client.thumbnail(int(event_id)), content_type="image/jpeg")
        if kind != "clip.mp4":
            raise web.HTTPNotFound
        url, headers = client.clip_request(int(event_id))
        async with client._session.get(url, headers=headers) as resp:  # noqa: SLF001 – the integration's own session
            if resp.status != 200:
                raise web.HTTPNotFound
            out = web.StreamResponse(headers={"Content-Type": "video/mp4"})
            await out.prepare(request)
            async for chunk in resp.content.iter_chunked(64 * 1024):
                await out.write(chunk)
            await out.write_eof()
            return out

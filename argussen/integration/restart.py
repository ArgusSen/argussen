"""When the ArgusSen app has put a new version of this integration on disk, Home Assistant still runs the old one
until it restarts. The integration notices it and shows "Restart required" under Settings → Repairs, with a button that
restarts Home Assistant – like other integrations. (Only when the app installed the integration: HACS does this itself.)"""

from __future__ import annotations

from pathlib import Path

from homeassistant.components import persistent_notification
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers import issue_registry as ir
from homeassistant.helpers.event import async_call_later

from .const import DOMAIN

MARKER = Path(__file__).parent / ".installed-by-argussen-app"  # written by the app: a checksum of the installed files
ISSUE = "restart_required"
NOTIFICATION = "argussen_restart"  # the app's own notification (for the first installation) – replaced by the repair


def _read() -> str | None:
    try:
        return MARKER.read_text().strip() or None
    except OSError:
        return None


async def async_remember_files(hass: HomeAssistant) -> None:
    """At start: which files Home Assistant has loaded."""
    hass.data.setdefault(DOMAIN, {})["files"] = await hass.async_add_executor_job(_read)


async def async_check_files(hass: HomeAssistant) -> None:
    """Called when ArgusSen comes back (e.g. after the app was updated): new files → Restart required."""
    loaded = hass.data.get(DOMAIN, {}).get("files")
    if loaded is None:
        return
    now = await hass.async_add_executor_job(_read)
    if now and now != loaded:
        ir.async_create_issue(
            hass, DOMAIN, ISSUE, is_fixable=True, is_persistent=False, severity=ir.IssueSeverity.WARNING,
            translation_key=ISSUE,
        )

        @callback
        def _dismiss(*_: object) -> None:
            persistent_notification.async_dismiss(hass, NOTIFICATION)

        # the app sends its notification about 10 seconds after it has started: remove it now and a little later
        _dismiss()
        async_call_later(hass, 30, _dismiss)
        async_call_later(hass, 90, _dismiss)

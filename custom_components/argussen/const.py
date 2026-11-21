"""Constants for the ArgusSen integration."""

DOMAIN = "argussen"

CONF_TOKEN = "token"
CONF_URL = "url"
CONF_INSTANCE = "instance"
CONF_STREAM_QUALITY = "stream_quality"  # "sd" (default, light) or "hd" for Home Assistant's camera cards

# Objects the AI finds, and motion alone (events from motion).
OBJECTS = ("person", "car", "van", "bicycle")
EVENT_TYPES = (*OBJECTS, "motion")

# Fired on Home Assistant's event bus when an event starts, gets new objects or ends.
BUS_EVENT = "argussen_event"


def signal_update(entry_id: str) -> str:
    """Dispatcher signal: the state of an ArgusSen server changed."""
    return f"{DOMAIN}_{entry_id}_update"

# ArgusSen – NVR for Home Assistant

Records your surveillance cameras 24/7, finds people, cars, vans and bicycles with AI, and shows live video,
a timeline and events right in the Home Assistant sidebar. Everything stays on your own hardware.

## Before you start

1. **Configuration tab → AI object detection** – choose how events are found:
   - `none` – no AI: events come from motion.
   - `external` – your own AI server (Blue Onyx or CodeProject.AI): fill in its address.
   - `builtin` – the AI runs inside this app (needs a fairly fast machine; the model is downloaded on first start).

   Press **Save**.
2. Turn on **Show in sidebar**, and press **Start**.
3. **Restart Home Assistant** once (a notification reminds you). Then add **ArgusSen** under
   *Settings → Devices & services → Discovered* – its cameras, sensors and events come to Home Assistant.

## Then

Open **ArgusSen** in the sidebar and go to **Settings → Cameras → New camera → Search the network for cameras**.
Reolink, ONVIF and RTSP cameras are supported.

Under **Settings → Storage** you choose where recordings are stored (Home Assistant's own disk or a network drive)
and for how long they are kept.

See the **Documentation** tab for everything else.

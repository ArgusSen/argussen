# ArgusSen – NVR for Home Assistant

ArgusSen records your surveillance cameras around the clock, finds people, cars, vans and
bicycles with AI, and shows live video, a 24/7 timeline and events directly in the Home Assistant
sidebar. Everything runs locally – no cloud, no subscription.

> ArgusSen is available in **English, German, French, Danish, Swedish, Norwegian and Dutch**.
> By default it uses the same language as Home Assistant – see [Language](#language).

## Getting started

1. On the app page, turn on **Show in sidebar**, start the app and open **ArgusSen** in the sidebar. You are logged in with your Home Assistant
   user – Home Assistant administrators are also ArgusSen administrators. Other Home Assistant users see
   no cameras until an administrator chooses which (see [Users](#users)).
2. Go to **Settings → Cameras → New camera**. Press **Search the network for cameras** and pick your camera
   (or type its address), choose the camera type, fill in username and password, press **Test connection**
   and save. Each camera has four
   sections, each with its own Save button: Connection, Who may see it and operation, Home Assistant,
   and Motion and AI.
3. Choose where recordings are stored and for how long under **Settings → Storage**.

## Cameras

ArgusSen records two streams from each camera: the **main stream** (full resolution, for recordings and HD
playback) and the **sub stream** (low resolution, at most 1280 pixels wide – used for motion detection, AI,
the 24/7 timeline and live view on phones). Almost all network cameras have both.

### Camera types

| Type | For | How |
|---|---|---|
| **Reolink** | Wired Reolink cameras | Optimised: Reolink's own protocol (bc, port 9000), HTTPS-FLV or RTSP, with automatic fallback. |
| **ONVIF** | Most other brands (Hikvision, Dahua, Amcrest, Axis, Uniview, TP-Link VIGI …) | ArgusSen logs in with ONVIF and finds the main and sub stream itself. ONVIF must be switched on in the camera (often port 80, 8000 or 8080); some brands need a separate ONVIF user. |
| **RTSP address** | Any camera with RTSP | Type the two addresses – or pick a brand preset (Hikvision, Dahua, Axis, TP-Link Tapo/VIGI, Uniview, Foscam). A path such as `/stream1` or a whole address `rtsp://…` / `rtsps://…` (e.g. from UniFi Protect). |

For Reolink cameras you can give a list of methods in order of preference (for example `bc,flv,rtsp`).
If a method fails, ArgusSen switches to the next one by itself and tries the preferred one again later.

### Searching the network

**Search the network for cameras** looks for cameras on the network Home Assistant is on (the /24 around its
address): it sends an ONVIF discovery to every address and checks the RTSP port. Cameras that are already added
are shown greyed out. Cameras on another network (for example a separate camera VLAN) can be found by typing
the network, such as `192.168.20.0/24`.

### Video format

H.264 works everywhere. H.265 (HEVC, e.g. 4K Reolink cameras) is recorded as it is and can be watched live and
played back in browsers and apps that can decode H.265 – most newer PCs (Chrome, Edge) and phones can. If a device
cannot, ArgusSen says so; then choose H.264 for the camera's main stream (in the camera's own app) if possible.

**Live delay**: the live view is a few seconds behind. It is shortest when the camera sends a key frame every second
(the *I-frame interval* or *GOP* in the camera's stream settings – some newer Reolink models send one only every 2–4
seconds, which makes the live view 6–10 seconds late).

Battery-powered cameras cannot record 24/7 and are not supported.

## Storage

Choose the location under **Settings → Storage**:

- **Home Assistant's own disk** (default) – recordings go to `/media/argus`.
- **A network share (NAS)** – first add it in Home Assistant under *Settings → System → Storage →
  Add network storage* with usage **Media**. It then appears in the list in ArgusSen, and
  recordings are stored in a folder called `argus` on the share.

If the network share stops responding, ArgusSen keeps recording on the local disk and moves the
recordings to the share when it is back.

When you change the storage, existing recordings are not moved: they can still be played back and are
deleted when they expire.

### How long recordings are kept

Set under **Settings → Storage → Keep recordings** (applies within 10 minutes, no restart):

- **Days of 24/7 (SD)** – low resolution.
- **Hours of full resolution (HD)** – the full resolution of all hours (0 = only around events).
- **Days of events** – events with HD video around them. Protected events are never deleted.

Until you choose, the default follows the storage:

| Storage | SD | HD | Events |
|---|---|---|---|
| Home Assistant's own disk below 128 GB | 3 days | 0 hours (HD only around events) | 30 days |
| Home Assistant's own disk of 128 GB or more | 14 days | 24 hours | 30 days |
| Network drive (NAS) | 14 days | 24 hours | 30 days |

**Use the default** goes back to it.

The page shows the expected space, calculated from your cameras' real file sizes, and warns if it does not fit.
Typical for one 4–5 MP camera: 3–6 GB SD and about 33 GB HD per day.

### Most space for ArgusSen

Under **Settings → Storage → Most space for ArgusSen** you can give ArgusSen a limit in GB (empty = no limit). When
the recordings take more, the oldest are deleted in the same order as below – never those around protected
events. How much ArgusSen uses is measured every 10 minutes and shown on the Storage page and the **Status** page
(the ring shows the use of the limit; the tiles show ArgusSen's use and the free space of the whole disk or share).

### Full disk protection

ArgusSen keeps free space on every storage place: on Home Assistant's own disk 10 % (at least 10 GB), so
Home Assistant's database, updates and backups always have room; on a network share 1 % (at least 5 GB).
If there is less, the oldest recordings are deleted first – HD outside events, then SD outside events, then
recordings around events that are not protected. Recordings around protected events and the newest 10 minutes
are never deleted. If only those are left, the Status page and the Home Assistant entity
`binary_sensor.argussen_storage_low` show a warning.

## AI (object detection)

Set **AI object detection** in the app's configuration:

| Option | What it does |
|---|---|
| `none` | No AI. Events come from motion (choose **Motion** under the camera's *Motion and AI*). |
| `external` | Use your own AI server with a CodeProject.AI-compatible API, such as [Blue Onyx](https://github.com/xnorpx/blue-onyx) or CodeProject.AI. Enter the address in **External AI server**. |
| `builtin` | Run the built-in AI (Blue Onyx) inside the app. Tested on 64-bit PCs (amd64); it is also built for Raspberry Pi (aarch64), but is slow there and not tested yet. |

With the built-in AI, the chosen model is downloaded from Hugging Face on first start
(`rf-detr-n` 114 MB or `rt-detrv2-s` 76 MB) and is kept out of Home Assistant backups.
It uses the CPU – on a modern PC about 0.15 s per image, which is enough for a handful of cameras.

Per camera you choose which objects create events: person, car, van, bicycle – and **Motion**, which
creates an event from motion alone (also without AI). Without AI, new cameras use Motion.

## Home Assistant integration

ArgusSen comes with a Home Assistant integration, which the app installs (and keeps up to date) by itself:

1. After the first start, Home Assistant shows a notification: **restart Home Assistant** once.
2. Go to **Settings → Devices & services**. Under **Discovered** you see **ArgusSen** – press **Add**.

That is all. For each camera you get a device with:

| Entity | What it is |
|---|---|
| Camera | Live video (in every Home Assistant camera card and the Home Assistant app) and a still image |
| Motion | On while there is motion |
| Person, Car, Van, Bicycle | On while an event with that object is going on |
| Detection (event entity) | Fires `person`, `car`, `van`, `bicycle` or `motion` when one is found – for automations |
| Latest event / Latest event picture | The time and the picture of the newest event |
| Connection | Whether the camera is recording |

And for ArgusSen itself: **Home mode** (a switch) and **Storage almost full**.

Each camera also has the switch **Active** (for administrators – the app's integration user is one): off switches the
camera off in ArgusSen (no recording, no events – like *Active* in the camera's settings), on switches it on again.
Use it in automations, for example to switch indoor cameras off while you are home. While a camera is off, only this
switch is shown for it.

The names follow Home Assistant's language. Recordings of events are in Home Assistant's **Media** browser
(ArgusSen → camera → event).

**Zones**: draw up to 3 zones and 3 areas to ignore per camera in ArgusSen under *Settings → Cameras → camera →
5 · Zones* – tap the corners on the picture, drag a corner to move it, give the zone a name and choose which objects count
in it (none = all). An AI hit counts in a zone when its foot point (the middle of the bottom edge of its box) is inside;
motion counts where the picture moves. Events get the names of their zones, and every zone gets
`binary_sensor.<camera>_<zone>` (on while something is in the zone, attribute `objects`) and
`event.<camera>_<zone>_detection`. Areas to ignore (a road, a tree in the wind) are left out of the motion detection and
the AI. Without zones everything works as before.

**Live video on a dashboard**: add a **Picture entity** (or Picture glance) card with the camera and set
**Camera view** to **Live** (`camera_view: live`) – the default shows a still image. Camera cards use the light SD
stream; choose HD under **Settings → Devices & services → ArgusSen → Configure**. Home Assistant only fetches one
stream per camera, however many screens show it, and stops it when nobody is watching.

**Blueprint – a message with the picture to your phone** (the easiest way): the app puts the blueprint
**"ArgusSen – notify when something is detected"** into Home Assistant. *Settings → Automations →
Create automation* → choose it, then pick the **camera**, **what** (person, car, van, bicycle, motion), optionally a
**zone** (a drop-down of the ArgusSen zones), the **phone** (with the Home Assistant app) and how often at most. With ArgusSen on a separate server, import it:
[![Import the blueprint](https://my.home-assistant.io/badges/blueprint_import.svg)](https://my.home-assistant.io/redirect/blueprint_import/?blueprint_url=https%3A%2F%2Fgithub.com%2FArgusSen%2Fargussen%2Fblob%2Fmain%2Fblueprints%2Fautomation%2Fargussen%2Fnotify.yaml)

**Automations** – your own: *Settings → Automations → Create automation → Add trigger → Device*, choose the
**camera** (not "ArgusSen", which is the app itself) and pick a trigger:

- **Person detected**, **Car detected**, **Van detected**, **Bicycle detected**, **Motion detected**
- with zones: **Person came into Driveway** (and the same for car, van, bicycle and motion) for each zone

Example – a notification with the picture when a person is seen at the front door (the same automation in YAML):

```yaml
triggers:
  - trigger: device
    domain: argussen
    device_id: <the front door camera's device>
    type: person
actions:
  - action: notify.mobile_app_my_phone
    data:
      message: "Somebody is at the front door"
      data:
        image: /api/camera_proxy/camera.front_door
```

For more control every event start, change and end is also sent on Home Assistant's event bus as `argussen_event`,
with `camera`, `event_id`, `labels`, `new_labels`, `zones`, `zone_names`, `zone_new`, `ongoing`, `start` and `end`.
The event entities (`event.<camera>_detection`, `event.<camera>_<zone>_detection`) can be used as triggers too.

The action **ArgusSen: Protect event** protects an event (or removes the protection).

**ArgusSen on a separate server**: install the integration from HACS (custom repository
`https://github.com/ArgusSen/argussen`, type *Integration*), then add **ArgusSen** under *Settings → Devices &
services* with the server's address (for example `http://192.168.1.20:8080`) and an ArgusSen user.

### MQTT (older way)

Before the integration, ArgusSen sent its entities through MQTT. This can still be switched on with the app
option **Home Assistant entities via MQTT** (needs the Mosquitto broker app). With the integration, leave it off –
otherwise you get every sensor twice.

## Users

You are logged in with your Home Assistant user. Each Home Assistant user gets an ArgusSen user the first
time it opens ArgusSen, linked to the Home Assistant user (renaming it in Home Assistant is fine):

- **Home Assistant administrators** are ArgusSen administrators and see all cameras. Admin rights are set in
  Home Assistant, not in ArgusSen.
- **Other users** see no cameras until an administrator chooses which under **Settings → Users** – or turns on
  *New Home Assistant users see all cameras* there.
- Everybody can protect an event; only administrators can remove the protection or delete events.
- If a user stops being an administrator in Home Assistant, its cameras are reset to what new Home Assistant
  users get – also if an ArgusSen administrator had given it all cameras. Choose its cameras again under
  **Settings → Users**.
- A user deleted in ArgusSen comes back when it opens ArgusSen again – remove the user in Home Assistant instead.

## Phone

The Home Assistant app works everywhere Home Assistant works (also via Home Assistant Cloud), and
ArgusSen is in its sidebar.

## Language

ArgusSen uses one language for the user interface, the log and the names of its Home Assistant entities.
By default it follows Home Assistant's language (or English, if ArgusSen does not have that language yet).
An administrator can choose another language under **Settings → General → Language**.

Each device can also show the user interface in its own language (**Language on this device**); by default it
uses ArgusSen's language.

## Log

The log (Settings → Log) is shown in ArgusSen's language. It is stored in English, so changing the language
also changes the lines that are already in the log. When asking for help, switching to English first makes the
log easier for others to read.

## Security and privacy

- **Who sees what**: see [Users](#users). Only administrators see the home mode (whether you are home),
  the log, the status page and the settings.
- **The integration** talks to ArgusSen with its own token, made by the app and given to Home Assistant through
  the Supervisor; it is not shown as a user. Home Assistant's video component cannot send a login, so only video
  addresses (live, playback, recordings) accept the token in the address.
- **MQTT** (only if you switch it on): the entities are sent through your MQTT broker. Everybody with access to the broker can read the
  camera still images and event images, and can switch the home mode (and thereby events) on and off – so
  access to the broker equals access to the cameras' images. Give MQTT logins only to devices you trust, or
  switch the images off under **Settings → General**. The images are not retained on the broker.
- **Backups**: Home Assistant backups of the app contain its database, including the cameras' usernames and
  passwords, and the MQTT login. Protect your backups (Home Assistant can encrypt them). Recordings and the AI
  model are not in the backups.
- **Camera connections**: Reolink's own protocol (bc) logs in without sending the password. RTSP usually uses
  digest login. Reolink's HTTPS-FLV sends the password over HTTPS without checking the camera's certificate
  (Reolink cameras use self-signed certificates), so it is the last method tried. ONVIF uses a password digest.
  Keep cameras on a network you trust (ideally a separate camera network).
  With RTSP and HTTPS-FLV the camera's address, including its password, is on the command line of the video process
  (ffmpeg) inside the app's container. Other apps cannot see it, unless an app has access to all of the machine's
  processes (e.g. system monitors such as Glances) – Reolink's own protocol (bc), tried first, does not have this.
- **Port 8099**: closed by default. If you open it (for the ArgusSen phone app), the API can be reached directly
  on your network, with ArgusSen's own login (users need a password set under Settings → Users; at most 5
  failed attempts per address and 10 per username in 15 minutes – so somebody who can reach the port can
  lock a known username out for 15 minutes). Home Assistant's own login is only used
  through the sidebar.
- **Built-in AI**: runs inside the app and only listens on 127.0.0.1. Images never leave your network.

## Troubleshooting

- **An update for ArgusSen cannot be installed** (the "Update available" badge does nothing): Home Assistant only
  creates the update for an app after it has noticed the app. On a new Home Assistant without other apps this can
  wait until the next restart. ArgusSen checks this itself a few minutes after it starts and asks Home Assistant to
  reload its Supervisor integration; if that does not help, a notification asks you to restart Home Assistant once
  (Settings → ⋮ → Restart Home Assistant).

## Support

Report problems at https://github.com/ArgusSen/argussen/issues

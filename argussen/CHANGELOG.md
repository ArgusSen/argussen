# Changelog

## 1.0.0
- First stable version.
- The old app options `sub_days`, `main_hours` and `event_days` (replaced in 0.9.0 by Settings → Storage in
  ArgusSen) are removed. Upgrading from a version before 0.9.0 with those options set? Check the retention under
  Settings → Storage after the upgrade.

## 0.10.1
- Searching the network: the brand is shown for more cameras (D-Link, Hikvision, Dahua, Axis, TP-Link …), the search
  is sent twice (answers over wifi get lost), and a camera that only answers RTSP is asked for ONVIF directly – so
  it is no longer shown as "unknown".
- Documentation: live video on a dashboard.

## 0.10.0
- **The ArgusSen integration for Home Assistant.** The app installs it by itself; after one restart of Home Assistant,
  ArgusSen is shown under *Discovered* and is set up with one click. Cameras with live video in every Home Assistant
  camera card, motion and object sensors, event entities and `argussen_event` for automations, the latest event,
  the home mode, "storage almost full", event recordings in the media browser, and the action *Protect event*.
- For ArgusSen on a separate server, the integration is also available through HACS.
- MQTT is now an option (`mqtt`, off by default) – with the integration it is not needed.
- Camera cards use the light SD stream by default; HD can be chosen in the integration's options.

## 0.9.7
- Events from motion alone no longer show "0 %".

## 0.9.6
- In Home Assistant in a phone's web browser, the bottom menu was hidden below the screen (Home Assistant makes the
  frame as tall as the screen including the address bar). ArgusSen now fits itself to the visible part.

## 0.9.5
- The default retention follows the storage: on Home Assistant's own disk below 128 GB, SD is kept 3 days and HD
  only around events (one 5 MP camera takes about 84 GB with 14 days SD + 24 hours HD); on a bigger disk or a network
  drive 14 days SD and 24 hours HD. Events are kept 30 days. **Use the default** under Settings → Storage goes back to it.

## 0.9.4
- The overview no longer shows "0 of 0 online" with a green dot before the first camera is added.

## 0.9.3
- No false "ERROR … Service not enabled" in the log when there is no MQTT broker.
- A new installation no longer logs "Recordings are kept … (set by the app options)".

## 0.9.2
- On a new Home Assistant, updates for ArgusSen could not be installed until Home Assistant was restarted (Home
  Assistant had not created the app's update entity). ArgusSen now checks this a few minutes after it starts and asks
  Home Assistant to reload its Supervisor integration – or shows a notification asking for one restart.

## 0.9.1
- Input fields and drop-down lists are dark grey with white text, like the rest of ArgusSen (they were white).
- The app's info tab tells you to choose the AI under Configuration before the first start.

## 0.9.0
**When upgrading:**
- The retention is now set in ArgusSen (Settings → Storage). Your values from the app options `sub_days`,
  `main_hours` and `event_days` are copied in at the first start, so no recordings are deleted early.
- Home Assistant users who are not administrators no longer see all cameras: they get what you choose for new
  Home Assistant users. **Check Settings → Users** and give each user the cameras they should see.
- The ArgusSen phone app needs the matching version (changes are now always sent as JSON).

Fixes from an independent code review:
- **Full disk protection** on every storage place (Home Assistant's own disk keeps 10 %, at least 10 GB, free):
  the oldest recordings are deleted first, never those around protected events. Warning on the Status page and
  the new entity `binary_sensor.argussen_storage_low`.
- **Home Assistant users** are linked by their Home Assistant id (not the name): an HA user can no longer take
  over an ArgusSen account with the same name, and renaming in HA keeps the user.
- **New Home Assistant users see no cameras** until an administrator chooses (or turns on "New Home Assistant
  users see all cameras" under Settings → Users). HA administrators see all.
- **Camera images via MQTT are no longer retained** on the broker, and can be switched off
  (Settings → General). DOCS has a new "Security and privacy" section.
- Login: the same response time whether a user exists or not; at most 10 failed attempts per username as well as
  5 per address; expired sessions are deleted.
- The connection test no longer sends a saved password to a new address.
- At most 2 clips are made at the same time, each at most 15 minutes.
- Only administrators can remove the protection of an event and see the home mode.
- Requests that change something must be JSON; security headers and a Content-Security-Policy on the web pages.
- Reolink's automatic method order is now bc → rtsp → flv (FLV sends the password without a certificate check).
- **Events from motion** – also without AI: choose "Motion" under the camera's Motion and AI.
- **How long recordings are kept** is set in ArgusSen (Settings → Storage), with the expected space – the app
  options `sub_days`, `main_hours` and `event_days` are removed.
- After a change of storage, the old recordings can still be played back and are deleted when they expire.
- Clearer help when a connection test fails, a message when the browser cannot play H.265, and admin rights of
  Home Assistant users are shown as set in Home Assistant.
- Changed rights of Home Assistant users apply at once (not after up to 5 minutes).
- When the storage is full of protected recordings only, the disk guard checks every 10 minutes instead of
  every 30 seconds (less load on a NAS).
- A hint when both Motion and AI objects are chosen (every motion then becomes an event).
- Tests for login, ingress trust, Home Assistant users, access per camera, the disk guard and the upgrade.

## 0.8.0
- Cameras of other brands: new camera types **ONVIF** (the main and sub stream are found automatically) and
  **RTSP address** (with presets for Hikvision, Dahua/Amcrest/Lorex, Axis, TP-Link Tapo/VIGI, Uniview and Foscam).
  Reolink cameras keep their optimised connection.
- **Search the network for cameras** when adding a camera (ONVIF discovery and RTSP), also on another network.
- The connection test shows the size and codec of both streams and warns about H.265.
- Fixed: deleting a camera did not remove it from Home Assistant.

## 0.7.0
- All code, identifiers and the database are now in English (existing data is migrated automatically on first start).
- New entity IDs with the prefix `argussen_` (for example `camera.argussen_door`, `switch.argussen_home_mode`,
  `binary_sensor.argussen_door_car`). The old `argus_*` entities are removed – update dashboards and automations.
- Direct links are `#camera/<id>`, `#event/<id>`, `#latest/<id>` and `#events` (the old links still work).
- Built-in AI: the model is pinned to a fixed version and its checksum is verified; Blue Onyx only listens on 127.0.0.1.
- The local fallback folder is now `/data/local`.
- One language for everything: ArgusSen follows Home Assistant's language by default (the user interface, the log
  and the entity names). An administrator can choose another language under Settings → General → Language, and each
  device can still choose its own. The option `ha_language` is removed.
- The log is stored in English and shown in the chosen language (also older lines after a language change).

## 0.6.0
- The app is available in English, German, French, Danish, Swedish, Norwegian and Dutch
  (Settings → General → Language; automatic by default).
- New option `ha_language`: language of the entity names in Home Assistant (entity IDs are unchanged).
- The log is written in English.
- The `ai` option values are now `none`, `external` and `builtin`.
- All documentation is in English.

## 0.5.3
- Camera settings are split into four sections, each with its own Save button.
- The bottom menu is shown on every page.

## 0.5.2
- Clearer logo: lighter aperture and eight larger eyes.

## 0.5.1
- The name ArgusSen is used everywhere in the app.
- Fixed: the Status page stopped and the Overview was blank when no cameras were added yet.

## 0.5.0
- New look: "Peacock" colour theme (petrol/turquoise) and a new logo – an aperture surrounded by twelve eyes,
  after Argus, the giant with a hundred eyes.

## 0.4.2
- Icon, documentation and English/Danish option texts. No personal defaults (AI address) in the code.

## 0.4.1
- Local fallback: if the network share (NAS) stops responding, recording continues on the
  local disk and is moved to the NAS when it is back. Oldest HD is deleted first when space runs low.
- Storage location is chosen inside ArgusSen (Settings → Storage).
- Settings is now a button in the bottom menu.

## 0.2.0
- Built-in AI (Blue Onyx + ONNX Runtime). The model is downloaded on first use.

## 0.1.0
- First test version: recording, live view, events, Home Assistant login and MQTT.

# Third-party software

ArgusSen itself is released under the MIT License (see `LICENSE`). It builds on the following
projects. Thank you to everyone behind them.

## Included in the app

| Project | Used for | License |
|---|---|---|
| [Blue Onyx](https://github.com/xnorpx/blue-onyx) by Marcus Asteborg | Built-in AI object detection (the `blue_onyx` program) | MIT |
| [ONNX Runtime](https://onnxruntime.ai) by Microsoft | Runs the AI models | MIT (+ its own third-party notices) |
| [hls.js](https://github.com/video-dev/hls.js) 1.5.20 (`argussen/web/hls.min.js`, unchanged release build) | Video playback in the browser | Apache-2.0 |
| [FFmpeg](https://ffmpeg.org) (Debian package) | Recording and live video | LGPL-2.1+ / GPL-2+ (Debian build). Source: https://packages.debian.org/source/trixie/ffmpeg |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) and modernc.org libraries | Database (pure Go SQLite) | BSD-3-Clause |
| Go standard library and golang.org/x | Server | BSD-3-Clause |
| github.com/google/uuid, remyoudompheng/bigfft | Dependencies of the database | BSD-3-Clause |
| github.com/dustin/go-humanize, mattn/go-isatty, ncruces/go-strftime | Dependencies of the database | MIT |
| Home Assistant base images | Container base (Debian) | Apache-2.0 / Debian licenses |

Blue Onyx is built from source and depends on about 360 Rust crates, all under permissive licenses
(MIT, Apache-2.0, BSD, ISC, Zlib, Unicode-3.0, CDLA-Permissive-2.0, Unlicense, CC0). The full list
is included in the app image as `/opt/argus/ai/LICENSES-blue-onyx-crates.txt`, together with
`LICENSE.blue-onyx`, `LICENSE.onnxruntime` and `ThirdPartyNotices.onnxruntime.txt`.

## Downloaded when you choose the built-in AI

The AI models are not part of ArgusSen. They are downloaded from Hugging Face on first use:

| Model | Source | Original project | License |
|---|---|---|---|
| rf-detr-n | [xnorpx/rf-detr](https://huggingface.co/xnorpx/rf-detr) | [RF-DETR](https://github.com/roboflow/rf-detr) by Roboflow | Apache-2.0 |
| rt-detrv2-s | [xnorpx/rt-detr2-onnx](https://huggingface.co/xnorpx/rt-detr2-onnx) | [RT-DETRv2](https://github.com/lyuwenyu/RT-DETR) | Apache-2.0 |

## Names

"Reolink" and "Argus" (a Reolink camera series) are trademarks of their owners, as are the other camera
brands named in ArgusSen (Hikvision, Annke, HiLook, Dahua, Amcrest, Lorex, Imou, Axis, TP-Link, Tapo, VIGI,
Uniview, Foscam, Ubiquiti, UniFi) and ONVIF. The brand names are only used to describe which cameras ArgusSen
can connect to. ArgusSen is an independent project and is not affiliated with or endorsed by any of them or by
Home Assistant.

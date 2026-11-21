# Built-in AI

The built-in AI is [Blue Onyx](https://github.com/xnorpx/blue-onyx) (MIT) running on
[ONNX Runtime](https://onnxruntime.ai) (MIT). ArgusSen starts it inside the app on
`127.0.0.1:32168` when the option `ai` is set to `builtin`, and talks to it with the
CodeProject.AI-compatible API (`POST /v1/vision/detection`).

No model is included in the app. On first start the chosen model (`rf-detr-n` or `rt-detrv2-s`)
is downloaded from Hugging Face to `/data/models` and excluded from backups. `run.sh` pins each
model to a fixed Hugging Face revision and checks its SHA-256, so a changed upload is never used.

## Building

The app's `Dockerfile` builds the AI in its own stage, for amd64 and aarch64 alike:

1. ONNX Runtime 1.32.0: `libonnxruntime.so` is taken from Microsoft's official Python wheel on PyPI
   (`manylinux_2_28`), and the wheel is checked with SHA-256 before it is used.
2. Blue Onyx is cloned at a fixed commit (v0.8.0), patched with
   [`blue-onyx-localhost.patch`](blue-onyx-localhost.patch) so it only listens on `127.0.0.1` (not on Home
   Assistant's internal app network), and built with `cargo build --locked --release`.
3. The licenses of Blue Onyx, ONNX Runtime and all Rust crates used by Blue Onyx are copied into the image
   (`/opt/argus/ai`).

Blue Onyx needs ONNX Runtime API ≥ 28, which is why the app uses Home Assistant's Debian base image.

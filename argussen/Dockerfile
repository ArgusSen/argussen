# ArgusSen – everything is built from source here, so the app can be built by Home Assistant itself (from this
# repository) and by the GitHub workflow (ready images). Versions and downloads are pinned and checked. Built natively per architecture (amd64, aarch64).

# ---- the ArgusSen server (Go, static) ----
FROM golang:1.27.4-trixie AS server
ARG BUILD_VERSION=dev
ARG GO_TAGS=
WORKDIR /src
COPY server/ ./
RUN CGO_ENABLED=0 go build -tags "${GO_TAGS}" -trimpath -ldflags "-s -w -X main.version=${BUILD_VERSION}" -o /out/argus .

# ---- built-in AI: Blue Onyx (MIT) on ONNX Runtime (MIT) ----
FROM rust:1.100-trixie AS ai
ARG TARGETARCH
ARG BLUE_ONYX_COMMIT=1cd4d93d95b99a4456b7c856c94fb5654c908ad2
RUN apt-get update \
    && apt-get install -y --no-install-recommends pkg-config libssl-dev unzip jq \
    && rm -rf /var/lib/apt/lists/*
# ONNX Runtime 1.32.0: the shared library from Microsoft's official Python wheel (PyPI), checked with SHA-256
RUN set -e; \
    case "${TARGETARCH}" in \
      amd64) W=a3/81/55845186c20cba59f614991f4065dea148e48eeb9749893768c85ce64327/onnxruntime-1.32.0-cp312-cp312-manylinux_2_28_x86_64.whl; \
             S=99e1074c4b2197accd5073052a465361be17b89c093b8ffb6e6bd8f986c52df8 ;; \
      arm64) W=4a/8f/1e43fb994513c7c7a9c6965272ef89d963c1bd4080993a6267bf913701d1/onnxruntime-1.32.0-cp312-cp312-manylinux_2_28_aarch64.whl; \
             S=e36c187006e7a7c8b8d6d57f1a209dd63f52430dca6c282b177718ad6b841054 ;; \
      *) echo "unsupported architecture ${TARGETARCH}"; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/ort.whl "https://files.pythonhosted.org/packages/${W}"; \
    echo "${S}  /tmp/ort.whl" | sha256sum -c -; \
    mkdir -p /ort /out; \
    unzip -j /tmp/ort.whl onnxruntime/capi/libonnxruntime.so.1.32.0 onnxruntime/LICENSE onnxruntime/ThirdPartyNotices.txt -d /ort; \
    ln -s libonnxruntime.so.1.32.0 /ort/libonnxruntime.so; \
    cp /ort/libonnxruntime.so.1.32.0 /out/libonnxruntime.so; \
    cp /ort/LICENSE /out/LICENSE.onnxruntime; cp /ort/ThirdPartyNotices.txt /out/ThirdPartyNotices.onnxruntime.txt; \
    rm /tmp/ort.whl
# Blue Onyx at a fixed commit, patched to listen on 127.0.0.1 only
COPY ai/blue-onyx-localhost.patch /tmp/
RUN set -e; \
    git clone --filter=blob:none https://github.com/xnorpx/blue-onyx.git /bo; \
    cd /bo; git checkout -q "${BLUE_ONYX_COMMIT}"; git apply /tmp/blue-onyx-localhost.patch; \
    BLUE_ONYX_ORT_LIB_DIR=/ort cargo build --locked --release --bin blue_onyx; \
    cp target/release/blue_onyx /out/; cp LICENSE /out/LICENSE.blue-onyx; \
    { echo "Rust crates used by Blue Onyx (name version license):"; \
      cargo metadata --format-version 1 --locked | jq -r '.packages | sort_by(.name)[] | "\(.name) \(.version) \(.license // "see crate")"'; \
    } > /out/LICENSES-blue-onyx-crates.txt; \
    rm -rf /bo /usr/local/cargo/registry

# ---- the Home Assistant app (the last stage = what Home Assistant builds) ----
FROM ghcr.io/home-assistant/base-debian:trixie-2026.11.0
ARG BUILD_ARCH
ARG BUILD_VERSION=dev
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg tzdata libssl3t64 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=server /out/argus /usr/bin/argus
COPY --from=ai /out/ /opt/argus/ai/
COPY web /opt/argus/web
COPY integration /opt/argus/integration
COPY blueprints /opt/argus/blueprints
COPY models.sh /opt/argus/models.sh
COPY run.sh /run.sh
RUN chmod a+x /run.sh /usr/bin/argus

LABEL io.hass.type="app" \
      io.hass.name="ArgusSen" \
      io.hass.version="${BUILD_VERSION}" \
      io.hass.arch="${BUILD_ARCH}" \
      org.opencontainers.image.licenses="MIT"

CMD ["/run.sh"]

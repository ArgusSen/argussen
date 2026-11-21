#!/usr/bin/with-contenv bashio
# Builds ArgusSen's configuration (/data/argus.json) from the app options and starts ArgusSen.
set -e

# The storage location is chosen inside ArgusSen (Settings → Storage); the default is Home Assistant's own disk.
STORAGE=/media/argus

# AI models: a fixed revision on Hugging Face and the SHA-256 of each file, so a changed upload is never used.
# model → "repository revision model-type onnx-sha256 yaml-sha256"
declare -A MODELS=(
  [rf-detr-n]="xnorpx/rf-detr 5deb550b9ebc2d24c0a1872a2af50692936eef4e rf-detr cdb8176a6169fa93c1896827e4e416c2190ed042649433b81d1fb5cc2a35ba96 17931822374d2a8143d97f5f875d9aa7d339624925669c490586226b8b79bec2"
  [rt-detrv2-s]="xnorpx/rt-detr2-onnx c7d9ed2f629cc8279ea0f6b84e1a86f7b82f0795 rt-detrv2 c849b46a43925d0c24d88c7cab096fc8158ade265b7be1ae433e3effe5758a6a ed029ee87019fa769269fcff15eda24a15c17dcea42b70f4dd0e3ab744503e7d"
)

# download_model <file> <url> <sha256>: downloads only if the file is missing or has the wrong checksum.
download_model() {
  local file="$1" url="$2" sum="$3"
  if [ -s "${file}" ] && echo "${sum}  ${file}" | sha256sum -c --status; then
    return 0
  fi
  bashio::log.info "Downloading the AI model $(basename "${file}") …"
  if curl -fsSL --retry 3 -o "${file}.tmp" "${url}" && echo "${sum}  ${file}.tmp" | sha256sum -c --status; then
    mv "${file}.tmp" "${file}"
  else
    rm -f "${file}.tmp" "${file}"
    bashio::log.error "Could not download $(basename "${file}") (or its checksum did not match)"
    return 1
  fi
}

AI=$(bashio::config 'ai')
AI_URL=""
case "${AI}" in
  external)
    AI_URL=$(bashio::config 'ai_url')
    [ -n "${AI_URL}" ] || bashio::log.warning "AI 'external' is selected, but ai_url is empty – running without AI"
    ;;
  builtin)
    if [ ! -x /opt/argus/ai/blue_onyx ]; then
      bashio::log.warning "Built-in AI is not available for this machine (${BUILD_ARCH:-$(uname -m)}) yet – running without AI"
    else
      MODEL=$(bashio::config 'ai_model')
      read -r REPO REV TYPE SUM_ONNX SUM_YAML <<< "${MODELS[${MODEL}]}"
      mkdir -p /data/models
      BASE="https://huggingface.co/${REPO}/resolve/${REV}"
      OK=1
      download_model "/data/models/${MODEL}.onnx" "${BASE}/${MODEL}.onnx" "${SUM_ONNX}" || OK=0
      download_model "/data/models/${MODEL}.yaml" "${BASE}/${MODEL}.yaml" "${SUM_YAML}" || OK=0
      # models that are no longer used are removed (they can be downloaded again)
      find /data/models -type f ! -name "${MODEL}.*" -delete
      if [ "${OK}" = 1 ]; then
        bashio::log.info "Starting built-in AI (Blue Onyx, model ${MODEL})"
        (
          cd /opt/argus/ai
          while true; do
            HOME=/data/models LD_LIBRARY_PATH=/opt/argus/ai ORT_DYLIB_PATH=/opt/argus/ai/libonnxruntime.so \
              ./blue_onyx --port 32168 --model "/data/models/${MODEL}.onnx" --object-detection-model-type "${TYPE}" \
              --log-level warn 2>&1 | sed -u 's/^/AI: /'
            bashio::log.warning "Built-in AI stopped – restarting in 10 s"
            sleep 10
          done
        ) &
        AI_URL="http://127.0.0.1:32168"
      fi
    fi
    ;;
esac

# The Home Assistant integration (custom_components/argussen) is how ArgusSen's cameras, sensors and events come to
# Home Assistant. It is installed and kept up to date here – unless it was installed another way (e.g. HACS): then it
# is left alone. Home Assistant uses a new version after its next restart (ArgusSen shows a notification).
INT_SRC=/opt/argus/integration
INT_DST=/homeassistant/custom_components/argussen
if [ -d /homeassistant ] && [ -d "${INT_SRC}" ]; then
  if [ ! -e "${INT_DST}" ] || [ -f "${INT_DST}/.installed-by-argussen-app" ]; then
    NEW=$(jq -r .version "${INT_SRC}/manifest.json")
    # the content decides (not only the version): the marker file holds a checksum of the installed files
    SUM=$(cd "${INT_SRC}" && find . -type f | sort | xargs sha256sum | sha256sum | cut -c1-16)
    if [ "${SUM}" != "$(cat "${INT_DST}/.installed-by-argussen-app" 2>/dev/null)" ]; then
      mkdir -p /homeassistant/custom_components
      rm -rf "${INT_DST}.new" && cp -r "${INT_SRC}" "${INT_DST}.new" && echo "${SUM}" > "${INT_DST}.new/.installed-by-argussen-app"
      rm -rf "${INT_DST}" && mv "${INT_DST}.new" "${INT_DST}"
      bashio::log.info "Installed the ArgusSen integration ${NEW} in Home Assistant (used after its next restart)"
      echo "${NEW}" > /data/integration_installed
    fi
  else
    bashio::log.info "The ArgusSen integration is installed another way (e.g. HACS) – left as it is"
  fi
fi

# MQTT (the older way to Home Assistant, before the integration): only when switched on in the options
MQTT='null'
# asked quietly: bashio::services.available logs an ERROR when there is no broker, which is not an error here
if bashio::config.true 'mqtt' && curl -fsS -H "Authorization: Bearer ${SUPERVISOR_TOKEN}" http://supervisor/services/mqtt >/dev/null 2>&1; then
  MQTT=$(jq -n --arg h "$(bashio::services mqtt 'host'):$(bashio::services mqtt 'port')" \
    --arg u "$(bashio::services mqtt 'username')" --arg p "$(bashio::services mqtt 'password')" \
    '{host:$h, user:$u, password:$p}')
  bashio::log.info "MQTT: $(bashio::services mqtt 'host')"
else
  bashio::config.true 'mqtt' && bashio::log.info "No MQTT broker – MQTT entities are not sent"
fi

jq -n --arg storage "${STORAGE}" --arg ai "${AI_URL}" --argjson mqtt "${MQTT}" \
  '{storage:$storage, data:"/data", fallback_storage:"/data/local", listen:":8099", web_dir:"/opt/argus/web",
    blueonyx:$ai, ingress:true,
    mqtt:$mqtt, cameras:[]}' \
  > /data/argus.json

bashio::log.info "Starting ArgusSen (AI: ${AI})"
exec /usr/bin/argus -config /data/argus.json

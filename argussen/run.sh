#!/usr/bin/with-contenv bashio
# Builds ArgusSen's configuration (/data/argus.json) from the app options and starts ArgusSen.
set -e

# The storage location is chosen inside ArgusSen (Settings → Storage); the default is Home Assistant's own disk.
STORAGE=/media/argus

# shared with the standalone server: the AI models and how the built-in AI is started
log_info() { bashio::log.info "$@"; }
log_error() { bashio::log.error "$@"; }
# shellcheck source=models.sh
. /opt/argus/models.sh

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
      if start_builtin_ai "$(bashio::config 'ai_model')" /data/models; then
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

# The blueprints (Settings → Automations → Create automation → "ArgusSen – notify when something is detected"): copied to
# Home Assistant's blueprints folder and kept up to date – a file the user has changed is left alone (marker with checksums).
BP_SRC=/opt/argus/blueprints
BP_DST=/homeassistant/blueprints/automation/argussen
if [ -d /homeassistant ] && [ -d "${BP_SRC}" ]; then
  mkdir -p "${BP_DST}"
  # the marker holds the checksum of the version the app installed – a file that differs from it was changed by the user
  MARK="${BP_DST}/.installed-by-argussen-app"
  : > "${MARK}.new"
  for f in "${BP_SRC}"/*.yaml; do
    name=$(basename "${f}")
    old_sum=$(grep "^${name} " "${MARK}" 2>/dev/null | cut -d' ' -f2 || true)  # no marker yet: ""
    if [ ! -e "${BP_DST}/${name}" ] || [ "$(sha256sum < "${BP_DST}/${name}" | cut -c1-16)" = "${old_sum}" ]; then
      cp "${f}" "${BP_DST}/${name}"
      echo "${name} $(sha256sum < "${f}" | cut -c1-16)" >> "${MARK}.new"
    else
      echo "${name} ${old_sum}" >> "${MARK}.new"  # changed by the user: left alone, also next time
    fi
  done
  # blueprints the app no longer has are removed – unless the user has changed them
  if [ -f "${MARK}" ]; then
    while read -r name sum; do
      if [ ! -e "${BP_SRC}/${name}" ] && [ -e "${BP_DST}/${name}" ] && [ "$(sha256sum < "${BP_DST}/${name}" | cut -c1-16)" = "${sum}" ]; then
        rm -f "${BP_DST}/${name}"
      fi
    done < "${MARK}"
  fi
  mv "${MARK}.new" "${MARK}"
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

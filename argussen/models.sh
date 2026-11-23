# AI models: a fixed revision on Hugging Face and the SHA-256 of each file, so a changed upload is never used.
# Used by the Home Assistant app (run.sh) – and by the start script of the standalone server.
# model → "repository revision model-type onnx-sha256 yaml-sha256"
declare -A MODELS=(
  [rf-detr-n]="xnorpx/rf-detr 5deb550b9ebc2d24c0a1872a2af50692936eef4e rf-detr cdb8176a6169fa93c1896827e4e416c2190ed042649433b81d1fb5cc2a35ba96 17931822374d2a8143d97f5f875d9aa7d339624925669c490586226b8b79bec2"
  [rt-detrv2-s]="xnorpx/rt-detr2-onnx c7d9ed2f629cc8279ea0f6b84e1a86f7b82f0795 rt-detrv2 c849b46a43925d0c24d88c7cab096fc8158ade265b7be1ae433e3effe5758a6a ed029ee87019fa769269fcff15eda24a15c17dcea42b70f4dd0e3ab744503e7d"
)

# download_model <file> <url> <sha256>: downloads only if the file is missing or has the wrong checksum.
# The caller defines log_info and log_error.
download_model() {
  local file="$1" url="$2" sum="$3"
  if [ -s "${file}" ] && echo "${sum}  ${file}" | sha256sum -c --status; then
    return 0
  fi
  log_info "Downloading the AI model $(basename "${file}") …"
  if curl -fsSL --retry 3 -o "${file}.tmp" "${url}" && echo "${sum}  ${file}.tmp" | sha256sum -c --status; then
    mv "${file}.tmp" "${file}"
  else
    rm -f "${file}.tmp" "${file}"
    log_error "Could not download $(basename "${file}") (or its checksum did not match)"
    return 1
  fi
}

# start_builtin_ai <model> <models-dir>: downloads the model (if needed) and runs Blue Onyx on 127.0.0.1:32168,
# restarted if it stops. Returns 1 if the model could not be downloaded.
start_builtin_ai() {
  local model="$1" dir="$2" repo rev type sum_onnx sum_yaml ok=1
  read -r repo rev type sum_onnx sum_yaml <<< "${MODELS[${model}]}"
  if [ -z "${repo}" ]; then
    log_error "Unknown AI model '${model}' (rf-detr-n or rt-detrv2-s)"
    return 1
  fi
  mkdir -p "${dir}"
  local base="https://huggingface.co/${repo}/resolve/${rev}"
  download_model "${dir}/${model}.onnx" "${base}/${model}.onnx" "${sum_onnx}" || ok=0
  download_model "${dir}/${model}.yaml" "${base}/${model}.yaml" "${sum_yaml}" || ok=0
  # models that are no longer used are removed (they can be downloaded again)
  find "${dir}" -type f ! -name "${model}.*" -delete
  [ "${ok}" = 1 ] || return 1
  log_info "Starting built-in AI (Blue Onyx, model ${model})"
  (
    cd /opt/argus/ai
    while true; do
      HOME="${dir}" LD_LIBRARY_PATH=/opt/argus/ai ORT_DYLIB_PATH=/opt/argus/ai/libonnxruntime.so \
        ./blue_onyx --port 32168 --model "${dir}/${model}.onnx" --object-detection-model-type "${type}" \
        --log-level warn 2>&1 | sed -u 's/^/AI: /'
      log_info "Built-in AI stopped – restarting in 10 s"
      sleep 10
    done
  ) &
}

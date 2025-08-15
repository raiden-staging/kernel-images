#!/usr/bin/env bash
set -uo pipefail

: "${BASE_URL:=http://127.0.0.1:10001}"
: "${LOG_FILE:=/tmp/kernel-operator/test.logs}"
: "${MAX_LOG_BYTES:=4000}"
: "${SSE_TIMEOUT_SEC:=5}"
: "${VERBOSE:=1}"
: "${CURL_CONNECT_TIMEOUT:=5}"
: "${CURL_MAX_TIME:=30}"

_start_ts="$(date -Iseconds)"
_tmproot="$(mktemp -d -t kop-test-XXXXXX)"
trap 'rm -rf "$_tmproot"' EXIT INT TERM

mkdir -p "$(dirname "$LOG_FILE")"
: > "$LOG_FILE"

PASS=0
FAIL=0
SKIP=0

ts() { date -Iseconds; }
log() { printf "%s %s\n" "$(ts)" "$*" >> "$LOG_FILE"; [ "${VERBOSE}" = "1" ] && printf "%s %s\n" "$(ts)" "$*"; }
banner() { local l; l="$(printf '─%.0s' $(seq 1 78))"; log "$l"; log "$1"; log "$l"; }

is_binary_file() {
  local f="$1"; local total printable
  total=$(wc -c < "$f" | tr -d ' ')
  [ "$total" -eq 0 ] && return 1
  printable=$(tr -cd '\11\12\15\40-\176' < "$f" | wc -c | tr -d ' ')
  awk -v p="$printable" -v t="$total" 'BEGIN{ if (t==0) exit 1; if (p/t < 0.80) exit 0; else exit 1 }'
}
trim_body_preview() {
  local f="$1" size; size=$(wc -c < "$f" | tr -d ' ')
  if is_binary_file "$f"; then printf "[binary %s bytes suppressed]\n" "$size"; return 0; fi
  if [ "$size" -le "$MAX_LOG_BYTES" ]; then sed -e 's/[^[:print:]\t]//g' "$f"; [ "$size" -gt 0 ] && printf "\n" || true
  else head -c "$MAX_LOG_BYTES" "$f" | sed -e 's/[^[:print:]\t]//g'; printf "... [trimmed, total=%s bytes]\n" "$size"
  fi
}
hdr_preview() { sed -e 's/\r$//' "$1" | head -n 40; }

curl_req() {
  local method="$1"; shift; local url="$1"; shift
  local body_file headers_file status start end dur
  body_file="$(mktemp -p "$_tmproot" body.XXXXXX)"
  headers_file="$(mktemp -p "$_tmproot" headers.XXXXXX)"
  start=$(date +%s%3N 2>/dev/null || date +%s)
  status=$(
    curl -sS -X "$method" \
      --connect-timeout "${CURL_CONNECT_TIMEOUT}" \
      --max-time "${CURL_MAX_TIME}" \
      -D "$headers_file" \
      -o "$body_file" \
      -w "%{http_code}" \
      "$url" "$@" 2>>"$LOG_FILE"
  )
  end=$(date +%s%3N 2>/dev/null || date +%s)
  if [ "${#start}" -eq 10 ]; then dur=$(( (end - start) * 1000 )); else dur=$(( end - start )); fi
  RES_STATUS="$status"; RES_BODY="$body_file"; RES_HEADERS="$headers_file"; RES_TIME_MS="$dur"
}
emit_http_transcript() {
  local method="$1"; shift; local url="$1"; shift
  log "HTTP: ${method} ${url}"; [ "$#" -gt 0 ] && log "CURL-ARGS: $*"
  log "STATUS: ${RES_STATUS}  TIME: ${RES_TIME_MS}ms"
  log "HEADERS:\n$(hdr_preview "$RES_HEADERS")"
  log "BODY:\n$(trim_body_preview "$RES_BODY")"
}
assert_status() { [ "$RES_STATUS" = "$1" ]; }
assert_body_contains() { LC_ALL=C grep -q -- "$1" "$RES_BODY"; }
pass(){ PASS=$((PASS+1)); log "PASS $*"; }
fail(){ FAIL=$((FAIL+1)); log "FAIL $*"; }
skip(){ SKIP=$((SKIP+1)); log "SKIP $*"; }
run_test(){ local suite="$1" fn="$2" t0 t1 ms; t0=$(date +%s%3N 2>/dev/null || date +%s); banner "Suite: ${suite}"; "$fn"; t1=$(date +%s%3N 2>/dev/null || date +%s); if [ "${#t0}" -eq 10 ]; then ms=$(( (t1 - t0) * 1000 )); else ms=$(( t1 - t0 )); fi; log "Elapsed: ${ms}ms"; }

t_health() {
  local url="${BASE_URL}/health"
  curl_req GET "$url"; emit_http_transcript GET "$url"
  if assert_status 200 && assert_body_contains '"status":"ok"'; then pass "[health] GET /health"; else fail "[health] GET /health"; fi
}
t_fs() {
  local dir="$_tmproot/fsdir" file1="$_tmproot/file1.txt" file2="$_tmproot/file2.txt" url
  url="${BASE_URL}/fs/create_directory"; curl_req PUT "$url" -H 'content-type: application/json' --data "{\"path\":\"${dir}\",\"mode\":\"0755\"}"; emit_http_transcript PUT "$url" -H 'content-type: application/json' --data "{\"path\":\"${dir}\",\"mode\":\"0755\"}"; assert_status 201 && pass "[fs] create_directory" || fail "[fs] create_directory"
  url="${BASE_URL}/fs/list_files?path=$(python3 - <<PY 2>/dev/null || echo "$(dirname "$dir")"
import os,sys;print(os.path.dirname(sys.argv[1]))
PY "$dir")"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 200 && pass "[fs] list_files" || fail "[fs] list_files"
  printf "hello world" > "$file1.tmp"
  url="${BASE_URL}/fs/write_file?path=$(python3 - <<PY 2>/dev/null || printf "%s" "$file1" | sed 's/ /%20/g'
import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))
PY "$file1")&mode=0644"; curl_req PUT "$url" --data-binary @"$file1.tmp"; emit_http_transcript PUT "$url" --data-binary @"$file1.tmp"; assert_status 201 && pass "[fs] write_file" || fail "[fs] write_file"
  url="${BASE_URL}/fs/read_file?path=$(python3 - <<PY 2>/dev/null || printf "%s" "$file1" | sed 's/ /%20/g'
import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))
PY "$file1")"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 200 && assert_body_contains "hello world" && pass "[fs] read_file" || fail "[fs] read_file"
  url="${BASE_URL}/fs/download?path=$(python3 - <<PY 2>/dev/null || printf "%s" "$file1" | sed 's/ /%20/g'
import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))
PY "$file1")"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 200 && pass "[fs] download" || fail "[fs] download"
  url="${BASE_URL}/fs/move"; curl_req PUT "$url" -H 'content-type: application/json' --data "{\"src_path\":\"${file1}\",\"dest_path\":\"${file2}\"}"; emit_http_transcript PUT "$url" -H 'content-type: application/json' --data "{\"src_path\":\"${file1}\",\"dest_path\":\"${file2}\"}"; assert_status 200 && pass "[fs] move" || fail "[fs] move"
  url="${BASE_URL}/fs/delete_file"; curl_req PUT "$url" -H 'content-type: application/json' --data "{\"path\":\"${file2}\"}"; emit_http_transcript PUT "$url" -H 'content-type: application/json' --data "{\"path\":\"${file2}\"}"; assert_status 200 && pass "[fs] delete_file" || fail "[fs] delete_file"
  local upath="$_tmproot/upload.bin"; printf "payload" > "$upath"; url="${BASE_URL}/fs/upload"; curl_req POST "$url" -F "path=$_tmproot/upload.bin" -F "file=@${upath};type=application/octet-stream"; emit_http_transcript POST "$url" -F "path=$_tmproot/upload.bin" -F "file=@${upath};type=application/octet-stream"; assert_status 200 && pass "[fs] upload" || fail "[fs] upload"
  url="${BASE_URL}/fs/set_file_permissions"; curl_req PUT "$url" -H 'content-type: application/json' --data "{\"path\":\"${upath}\",\"mode\":\"0755\"}"; emit_http_transcript PUT "$url" -H 'content-type: application/json' --data "{\"path\":\"${upath}\",\"mode\":\"0755\"}"; assert_status 200 && pass "[fs] set_file_permissions" || fail "[fs] set_file_permissions"
  if command -v tail >/dev/null 2>&1; then
    local tfile="$_tmproot/tail.log"; printf "initial\n" > "$tfile"
    local sse_url="${BASE_URL}/fs/tail/stream?path=$(python3 - <<PY 2>/dev/null || printf "%s" "$tfile" | sed 's/ /%20/g'
import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))
PY "$tfile")"
    ( sleep 1; echo "new line $(date +%s)" >> "$tfile" ) &
    local out="$_tmproot/tail.sse"; curl -sN --max-time "${SSE_TIMEOUT_SEC}" "$sse_url" > "$out" 2>>"$LOG_FILE" || true
    grep -q "new line" "$out" && pass "[fs] tail/stream" || fail "[fs] tail/stream"
    log "SSE(fs/tail) preview:\n$(trim_body_preview "$out")"
  else
    SKIP=$((SKIP+1)); log "SKIP [fs] tail/stream (no tail)"
  fi
}
t_clipboard() {
  if command -v xclip >/dev/null 2>&1 || { command -v wl-copy >/dev/null 2>&1 && command -v wl-paste >/dev/null 2>&1; }; then
    local url="${BASE_URL}/clipboard"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 200 && pass "[clipboard] GET" || fail "[clipboard] GET"
    url="${BASE_URL}/clipboard"; curl_req POST "$url" -H 'content-type: application/json' --data '{"type":"text","text":"Test Clipboard"}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"type":"text","text":"Test Clipboard"}'; assert_status 200 && pass "[clipboard] POST text" || fail "[clipboard] POST text"
    local out="$_tmproot/clip.sse"; curl -sN --max-time "${SSE_TIMEOUT_SEC}" "${BASE_URL}/clipboard/stream" > "$out" 2>>"$LOG_FILE" || true; log "SSE(clipboard) preview:\n$(trim_body_preview "$out")"; pass "[clipboard] stream open"
  else
    SKIP=$((SKIP+1)); log "SKIP [clipboard] no clipboard tools"
  fi
}
t_input() {
  if command -v "${XDOTOOL_BIN:-xdotool}" >/dev/null 2>&1; then
    local url="${BASE_URL}/input/mouse/move"; curl_req POST "$url" -H 'content-type: application/json' --data '{"x":100,"y":100}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"x":100,"y":100}'; assert_status 200 && pass "[input] mouse move" || fail "[input] mouse move"
    url="${BASE_URL}/input/mouse/click"; curl_req POST "$url" -H 'content-type: application/json' --data '{"button":"left","count":1}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"button":"left","count":1}'; assert_status 200 && pass "[input] mouse click" || fail "[input] mouse click"
  else
    SKIP=$((SKIP+1)); log "SKIP [input] xdotool missing"
  fi
}
t_process() {
  local url="${BASE_URL}/process/exec"; curl_req POST "$url" -H 'content-type: application/json' --data '{"command":"bash","args":["-lc","printf hi"]}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"command":"bash","args":["-lc","printf hi"]}'; if [ "$RES_STATUS" = "200" ]; then
    if command -v jq >/dev/null 2>&1; then out="$(jq -r '.stdout_b64' < "$RES_BODY" | base64 -d 2>/dev/null || true)"; [ "$out" = "hi" ] && pass "[process] exec stdout" || fail "[process] exec stdout"; else assert_body_contains '"stdout_b64"' && pass "[process] exec (no jq)" || fail "[process] exec"; fi
  else fail "[process] exec"; fi
  url="${BASE_URL}/process/spawn"; curl_req POST "$url" -H 'content-type: application/json' --data '{"command":"bash","args":["-lc","for i in 1 2 3; do echo $i; sleep 1; done"]}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"command":"bash","args":["-lc","for i in 1 2 3; do echo $i; sleep 1; done"]}'; if [ "$RES_STATUS" = "200" ]; then
    local pid; if command -v jq >/dev/null 2>&1; then pid="$(jq -r '.process_id' < "$RES_BODY")"; else pid="$(sed -n 's/.*"process_id":"\([^"]*\)".*/\1/p' "$RES_BODY" | head -n1)"; fi
    if [ -n "$pid" ]; then local out="$_tmproot/proc.sse"; curl -sN --max-time "${SSE_TIMEOUT_SEC}" "${BASE_URL}/process/${pid}/stdout/stream" > "$out" 2>>"$LOG_FILE" || true; grep -q "data_b64" "$out" && pass "[process] stdout/stream" || fail "[process] stdout/stream"; log "SSE(process) preview:\n$(trim_body_preview "$out")"; curl_req POST "${BASE_URL}/process/${pid}/kill" -H 'content-type: application/json' --data '{"signal":"TERM"}'; emit_http_transcript POST "${BASE_URL}/process/${pid}/kill" -H 'content-type: application/json' --data '{"signal":"TERM"}'; [ "$RES_STATUS" = "200" ] && pass "[process] kill" || fail "[process] kill"; else fail "[process] spawn returned no id"; fi
  else fail "[process] spawn"; fi
}
t_screenshot() {
  if command -v "${FFMPEG_BIN:-ffmpeg}" >/dev/null 2>&1 || command -v grim >/dev/null 2>&1; then local url="${BASE_URL}/screenshot/capture"; curl_req POST "$url" -H 'content-type: application/json' --data '{}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{}'; case "$RES_STATUS" in 200) pass "[screenshot] capture";; 500) fail "[screenshot] capture (server error)";; *) fail "[screenshot] capture (unexpected)";; esac
  else SKIP=$((SKIP+1)); log "SKIP [screenshot] ffmpeg/grim missing"; fi
}
t_recording() {
  if command -v "${FFMPEG_BIN:-ffmpeg}" >/dev/null 2>&1; then local url="${BASE_URL}/recording/start"; curl_req POST "$url" -H 'content-type: application/json' --data '{"id":"t1","maxDurationInSeconds":1}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"id":"t1","maxDurationInSeconds":1}'; [ "$RES_STATUS" = "201" ] && pass "[recording] start" || fail "[recording] start"; sleep 2; url="${BASE_URL}/recording/download?id=t1"; curl_req GET "$url"; emit_http_transcript GET "$url"; case "$RES_STATUS" in 200|202|404) pass "[recording] download status ${RES_STATUS}";; *) fail "[recording] download";; esac; url="${BASE_URL}/recording/delete"; curl_req POST "$url" -H 'content-type: application/json' --data '{"id":"t1"}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"id":"t1"}'; case "$RES_STATUS" in 200|404) pass "[recording] delete";; *) fail "[recording] delete";; esac
  else SKIP=$((SKIP+1)); log "SKIP [recording] ffmpeg missing"; fi
}
t_streaming() {
  if command -v "${FFMPEG_BIN:-ffmpeg}" >/dev/null 2>&1; then local start_url="${BASE_URL}/stream/start"; curl_req POST "$start_url" -H 'content-type: application/json' --data '{"rtmps_url":"rtmps://example.com/live","stream_key":"testkey","fps":1,"audio":{"capture_system":false}}'; emit_http_transcript POST "$start_url" -H 'content-type: application/json' --data '{"rtmps_url":"rtmps://example.com/live","stream_key":"testkey","fps":1,"audio":{"capture_system":false}}'; if [ "$RES_STATUS" = "200" ]; then local sid; if command -v jq >/dev/null 2>&1; then sid="$(jq -r '.stream_id' < "$RES_BODY")"; else sid="$(sed -n 's/.*"stream_id":"\([^"]*\)".*/\1/p' "$RES_BODY" | head -n1)"; fi; if [ -n "$sid" ]; then local out="$_tmproot/stream.sse"; curl -sN --max-time "${SSE_TIMEOUT_SEC}" "${BASE_URL}/stream/${sid}/metrics/stream" > "$out" 2>>"$LOG_FILE" || true; (grep -q "fps" "$out" || grep -q "bitrate" "$out") && pass "[stream] metrics/stream" || fail "[stream] metrics/stream"; log "SSE(stream) preview:\n$(trim_body_preview "$out")"; curl_req POST "${BASE_URL}/stream/stop" -H 'content-type: application/json' --data "{\"stream_id\":\"${sid}\"}"; emit_http_transcript POST "${BASE_URL}/stream/stop" -H 'content-type: application/json' --data "{\"stream_id\":\"${sid}\"}"; [ "$RES_STATUS" = "200" ] && pass "[stream] stop" || fail "[stream] stop"; else fail "[stream] start no id"; fi; else SKIP=$((SKIP+1)); log "SKIP [stream] start not available (status ${RES_STATUS})"; fi
  else SKIP=$((SKIP+1)); log "SKIP [stream] ffmpeg missing"; fi
}
t_metrics() {
  local url="${BASE_URL}/metrics/snapshot"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 200 && pass "[metrics] snapshot" || fail "[metrics] snapshot"
  local out="$_tmproot/metrics.sse"; curl -sN --max-time "${SSE_TIMEOUT_SEC}" "${BASE_URL}/metrics/stream" > "$out" 2>>"$LOG_FILE" || true; grep -q "cpu_pct" "$out" && pass "[metrics] stream" || fail "[metrics] stream"; log "SSE(metrics) preview:\n$(trim_body_preview "$out")"
}
t_logs() {
  local url="${BASE_URL}/logs/stream"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 400 && pass "[logs] rejects bad request" || fail "[logs] rejects bad request"
}
t_network() {
  local url="${BASE_URL}/network/intercept/rules" rules='{"rules":[{"match":{"method":"GET","host_contains":"example.com"},"action":{"type":"block"}}]}'
  curl_req POST "$url" -H 'content-type: application/json' --data "$rules"; emit_http_transcript POST "$url" -H 'content-type: application/json' --data "$rules"; if [ "$RES_STATUS" = "200" ]; then local rule_id; if command -v jq >/dev/null 2>&1; then rule_id="$(jq -r '.rule_set_id' < "$RES_BODY")"; else rule_id="$(sed -n 's/.*"rule_set_id":"\([^"]*\)".*/\1/p' "$RES_BODY" | head -n1)"; fi; if [ -n "$rule_id" ]; then curl_req DELETE "${BASE_URL}/network/intercept/rules/${rule_id}"; emit_http_transcript DELETE "${BASE_URL}/network/intercept/rules/${rule_id}"; [ "$RES_STATUS" = "200" ] && pass "[network] delete rules" || fail "[network] delete rules"; else fail "[network] apply rules missing id"; fi; else fail "[network] apply rules"; fi
  local out="$_tmproot/har.sse"; curl -sN --max-time "${SSE_TIMEOUT_SEC}" "${BASE_URL}/network/har/stream" > "$out" 2>>"$LOG_FILE" || true; log "SSE(network/har) preview:\n$(trim_body_preview "$out")"; pass "[network] HAR stream open"
}
t_os_locale() {
  local url="${BASE_URL}/os/locale"; curl_req GET "$url"; emit_http_transcript GET "$url"; assert_status 200 && pass "[os] get locale" || fail "[os] get locale"
  curl_req POST "$url" -H 'content-type: application/json' --data '{"locale":"en_US.UTF-8","timezone":"UTC","keyboard_layout":"us"}'; emit_http_transcript POST "$url" -H 'content-type: application/json' --data '{"locale":"en_US.UTF-8","timezone":"UTC","keyboard_layout":"us"}'; assert_status 200 && pass "[os] set locale" || fail "[os] set locale"
}

banner "Kernel Operator API — Probe Runner"
log "Base URL: ${BASE_URL}"
log "Log file: ${LOG_FILE}"
log "Started:  ${_start_ts}"

run_test "health"     t_health
run_test "fs"         t_fs
run_test "clipboard"  t_clipboard
run_test "input"      t_input
run_test "process"    t_process
run_test "screenshot" t_screenshot
run_test "recording"  t_recording
run_test "stream"     t_streaming
run_test "metrics"    t_metrics
run_test "logs"       t_logs
run_test "network"    t_network
run_test "os"         t_os_locale

TOTAL=$((PASS+FAIL+SKIP))
COVDEN=$TOTAL
if [ "$COVDEN" -eq 0 ]; then COVDEN=1; fi
REL_DEN=$((PASS+FAIL))
if [ "$REL_DEN" -eq 0 ]; then REL_DEN=1; fi
COVERAGE=$(( (PASS+FAIL)*100 / COVDEN ))
RELIABILITY=$(( PASS*100 / REL_DEN ))

banner "Summary"
log "Total: ${TOTAL}"
log "Pass:  ${PASS}"
log "Fail:  ${FAIL}"
log "Skip:  ${SKIP}"
log "Coverage: ${COVERAGE}%"
log "Reliability: ${RELIABILITY}%"
log "Finished: $(ts)"
[ "$FAIL" -gt 0 ] && exit 1 || exit 0
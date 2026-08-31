#!/usr/bin/env bash
#
# e2e_halo.sh — End-to-end test of jar2native with two REAL applications.
#
# Downloads (or reuses a local cache of) the official Halo release JARs and
# verifies the complete jar2native workflow for each:
#
#   1. package  jar2native <app.jar> (full JRE mode)
#   2. start    run the generated self-contained binary
#   3. serve    poll HTTP until the web server answers
#   4. stop     SIGTERM → verify graceful shutdown and clean process exit
#
# Usage:
#   scripts/e2e_halo.sh                  # run from the repo root
#
# Environment overrides:
#   J2N_JDK17   path to a JDK ≥17 (for halo-1.2.0)     [default: auto-detect]
#   J2N_JDK21   path to a JDK ≥21 (for halo-2.26.0)    [default: auto-detect]
#   J2N_CACHE   download/cache directory                [default: .e2e-cache]
#
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

J2N_CACHE="${J2N_CACHE:-$REPO_ROOT/.e2e-cache}"
HALO1_URL="https://github.com/halo-dev/halo/releases/download/v1.2.0/halo-1.2.0.jar"
HALO2_URL="https://github.com/halo-dev/halo/releases/download/v2.26.0/halo-2.26.0.jar"

PASS=()
FAIL=()

log()  { printf '\033[36m[e2e]\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m[e2e][PASS]\033[0m %s\n' "$*"; }
fail() { printf '\033[31m[e2e][FAIL]\033[0m %s\n' "$*"; }

# ── prerequisites ────────────────────────────────────────────────────────────

need() { command -v "$1" >/dev/null 2>&1 || { fail "missing prerequisite: $1"; exit 1; }; }
need go
need curl

if [[ ! -x ./jar2native ]]; then
  log "building ./jar2native"
  CGO_ENABLED=0 go build -o jar2native . || { fail "go build failed"; exit 1; }
fi

# find_jdk <min_major> — prints a JDK home whose java -version major is >= min.
find_jdk() {
  local min="$1"
  local candidates=()

  [[ -n "${JAVA_HOME:-}" ]] && candidates+=("$JAVA_HOME")
  candidates+=(
    /opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home
    /opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home
    /opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
    /usr/lib/jvm/*java* /usr/lib/jvm/*/ "$HOME"/.jdks/*
  )

  local c java_bin major
  for c in "${candidates[@]}"; do
    [[ -d "$c" ]] || continue
    java_bin="$c/bin/java"
    [[ -x "$java_bin" ]] || continue
    major="$("$java_bin" -version 2>&1 | head -1 | sed -E 's/.*"([0-9]+)\..*/\1/;s/.*"1\.([0-9]+)\..*/\1/')"
    [[ "$major" =~ ^[0-9]+$ ]] || continue
    if (( major >= min )); then
      echo "$c"
      return 0
    fi
  done
  return 1
}

# fetch_jar <url> <filename> — download with local cache (~/Downloads first).
fetch_jar() {
  local url="$1"
  local name="$2"
  local dest="$J2N_CACHE/$name"
  if [[ -f "$dest" ]]; then
    log "using cached $name" >&2
    echo "$dest"
    return 0
  fi
  if [[ -f "$HOME/Downloads/$name" ]]; then
    log "reusing $HOME/Downloads/$name" >&2
    echo "$HOME/Downloads/$name"
    return 0
  fi
  log "downloading $name from $url" >&2
  mkdir -p "$J2N_CACHE"
  if curl -fL --retry 3 -o "$dest.part" "$url" && mv "$dest.part" "$dest"; then
    echo "$dest"
    return 0
  fi
  rm -f "$dest.part"
  return 1
}

# wait_http <url> <timeout_s> — poll until the server responds (non-000).
wait_http() {
  local url="$1" timeout="$2" waited=0 code
  while (( waited < timeout )); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$url" 2>/dev/null || true)"
    if [[ -n "$code" && "$code" != "000" ]]; then
      echo "$code"
      return 0
    fi
    sleep 3
    waited=$((waited + 3))
  done
  echo "000"
  return 1
}

# test_app <jar_path> <jdk_home> <port> <label>
test_app() {
  local jar="$1" jdk="$2" port="$3" label="$4"
  local out log_file code waited pid exe
  log_file="/tmp/j2n-e2e-$label.log"
  log "── $label: packaging with JDK $jdk"
  out="$(./jar2native "$jar" --jdk-path "$jdk" --skip-smoke-test 2>&1)"
  if [[ $? -ne 0 ]] || [[ "$out" != *"Success"* ]]; then
    fail "$label: packaging failed"
    printf '%s\n' "$out" | tail -15
    FAIL+=("$label:package")
    return
  fi
  ok "$label: packaged ($(echo "$out" | grep -oE '\([0-9.]+ MB[^)]*\)' | head -1))"

  exe="dist/$(basename "${jar%.jar}")"
  chmod +x "$exe"

  log "── $label: starting on port $port"
  rm -f "$log_file"
  "$exe" --server.port="$port" >"$log_file" 2>&1 &
  local pid=$!

  code="$(wait_http "http://127.0.0.1:$port/" 120)" || true
  if [[ "$code" == "000" ]]; then
    fail "$label: HTTP server never came up"
    tail -5 "$log_file" | sed 's/^/      /'
    kill -9 "$pid" 2>/dev/null || true
    FAIL+=("$label:http")
    return
  fi
  ok "$label: HTTP $code on 127.0.0.1:$port"

  log "── $label: SIGTERM graceful shutdown"
  kill -TERM "$pid" 2>/dev/null || true
  waited=0
  while ps -p "$pid" >/dev/null 2>&1 && (( waited < 30 )); do
    sleep 1
    waited=$((waited + 1))
  done
  if ps -p "$pid" >/dev/null 2>&1; then
    fail "$label: process did not exit after SIGTERM"
    kill -9 "$pid" 2>/dev/null || true
    FAIL+=("$label:shutdown")
    return
  fi
  ok "$label: exited cleanly after SIGTERM"
  PASS+=("$label")
}

# ── run ──────────────────────────────────────────────────────────────────────

JDK17="${J2N_JDK17:-$(find_jdk 17 || true)}"
JDK21="${J2N_JDK21:-$(find_jdk 21 || true)}"

[[ -n "$JDK17" ]] || { fail "no JDK ≥ 17 found for halo-1.2.0 (set J2N_JDK17)"; exit 1; }
[[ -n "$JDK21" ]] || { fail "no JDK ≥ 21 found for halo-2.26.0 (set J2N_JDK21)"; exit 1; }
log "JDK for halo-1.2.0: $JDK17"
log "JDK for halo-2.26.0: $JDK21"

rm -rf dist
mkdir -p dist

HALO1_JAR="$(fetch_jar "$HALO1_URL" "halo-1.2.0.jar")" || { fail "cannot obtain halo-1.2.0.jar"; exit 1; }
HALO2_JAR="$(fetch_jar "$HALO2_URL" "halo-2.26.0.jar")" || { fail "cannot obtain halo-2.26.0.jar"; exit 1; }

test_app "$HALO1_JAR" "$JDK17" 18201 "halo-1.2.0"
test_app "$HALO2_JAR" "$JDK21" 18202 "halo-2.26.0"

# ── summary ──────────────────────────────────────────────────────────────────

echo
log "──────── summary ────────"
if (( ${#PASS[@]} )); then
  for p in "${PASS[@]}"; do ok "$p — full lifecycle passed"; done
fi
if (( ${#FAIL[@]} )); then
  for f in "${FAIL[@]}"; do fail "$f"; done
  exit 1
fi
log "all real-application e2e tests passed"
exit 0

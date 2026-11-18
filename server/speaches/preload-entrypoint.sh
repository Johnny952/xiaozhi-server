#!/bin/sh
# Starts the upstream server (CMD passed as "$@"), then downloads the models
# once it is healthy. Preload failures are logged but never stop the server.
MODELS="Systran/faster-whisper-small speaches-ai/piper-es_MX-claude-high"
BASE="http://localhost:${UVICORN_PORT:-8000}"

"$@" &
SERVER_PID=$!
trap 'kill -TERM "$SERVER_PID" 2>/dev/null' TERM INT

(
  i=0
  until curl -fsS "$BASE/health" >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -gt 120 ]; then echo "preload: server never became healthy" >&2; exit 0; fi
    sleep 1
  done
  for m in $MODELS; do
    echo "preload: ensuring model $m"
    # POST is idempotent: 200/201 on download, already-installed is not fatal.
    code=$(curl -sS -o /tmp/preload.out -w '%{http_code}' -X POST "$BASE/v1/models/$m" 2>&1) || true
    echo "preload: $m -> HTTP $code"
    case "$code" in 2*|409) ;; *) head -c 300 /tmp/preload.out >&2 2>/dev/null; echo >&2;; esac
  done
  echo "preload: done"
) &

wait "$SERVER_PID"
STATUS=$?
# A trapped signal interrupts `wait`; reap the server before exiting.
if kill -0 "$SERVER_PID" 2>/dev/null; then wait "$SERVER_PID"; STATUS=$?; fi
exit "$STATUS"

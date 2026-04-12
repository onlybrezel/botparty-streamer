#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <env-file-path>" >&2
  exit 1
fi

OUT_FILE="$1"

: "${TOKEN_ENDPOINT:?TOKEN_ENDPOINT required}"
: "${LK_ROOM:?LK_ROOM required}"
: "${LK_IDENTITY:?LK_IDENTITY required}"
: "${LK_NAME:?LK_NAME required}"
: "${TOKEN_TTL_SECONDS:=300}"

REQUEST_BODY=$(cat <<JSON
{"room":"${LK_ROOM}","identity":"${LK_IDENTITY}","name":"${LK_NAME}","ttlSeconds":${TOKEN_TTL_SECONDS}}
JSON
)

AUTH_HEADER=()
if [[ -n "${TOKEN_AUTH_BEARER:-}" ]]; then
  AUTH_HEADER=(-H "Authorization: Bearer ${TOKEN_AUTH_BEARER}")
fi

DEVICE_HEADERS=()
if [[ -n "${DEVICE_ID:-}" ]]; then
  DEVICE_HEADERS+=(-H "X-Device-Id: ${DEVICE_ID}")
fi
if [[ -n "${DEVICE_SECRET:-}" ]]; then
  DEVICE_HEADERS+=(-H "X-Device-Secret: ${DEVICE_SECRET}")
fi

RESP=$(curl -sS -X POST "${TOKEN_ENDPOINT}" \
  -H "Content-Type: application/json" \
  "${AUTH_HEADER[@]}" \
  "${DEVICE_HEADERS[@]}" \
  -d "${REQUEST_BODY}")

TOKEN=$(printf '%s' "${RESP}" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("token",""))')
if [[ -z "${TOKEN}" ]]; then
  echo "token missing in response: ${RESP}" >&2
  exit 1
fi

cat > "${OUT_FILE}" <<ENV
LK_TOKEN=${TOKEN}
ENV

echo "wrote short-lived token to ${OUT_FILE}"

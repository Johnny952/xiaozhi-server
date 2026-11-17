#!/bin/sh
set -eu

# envsubst silently blanks unset vars; fail loudly instead (set -u aborts).
: "${LAN_IP:?LAN_IP must be set}" "${LITELLM_MASTER_KEY:?LITELLM_MASTER_KEY must be set}"

# Render the config template into the private config the server expects in its
# working directory (/app/.config.yaml), so it never hits the first-boot exit.
envsubst '${LAN_IP} ${LITELLM_MASTER_KEY}' < /config/config.template.yaml > /app/.config.yaml
chmod 600 /app/.config.yaml

if grep -q '\${' /app/.config.yaml; then
  echo "entrypoint: unresolved \${...} placeholder left in /app/.config.yaml (is LAN_IP / LITELLM_MASTER_KEY set?)" >&2
  exit 1
fi

exec /app/xiaozhi-server

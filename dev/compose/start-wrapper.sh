#!/bin/sh
set -eu

mkdir -p /data/users
while [ ! -s /var/syncthing/config/config.xml ]; do
  sleep 1
done

APP_SYNCTHING_API_KEY=$(awk -F '[<>]' '/<apikey>/{print $3; exit}' /var/syncthing/config/config.xml)
if [ -z "$APP_SYNCTHING_API_KEY" ]; then
  printf '%s\n' 'Syncthing API key not found in config.xml' >&2
  exit 1
fi
export APP_SYNCTHING_API_KEY
exec /usr/local/bin/syncthing-server

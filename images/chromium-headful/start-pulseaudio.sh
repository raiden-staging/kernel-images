#!/bin/bash

set -o pipefail -o errexit -o nounset

echo "[pulse] Setting up permissions"
chown -R kernel:kernel /home/kernel/ /home/kernel/.config /etc/pulse || true
chmod 777 /home/kernel/.config /etc/pulse || true
chown -R kernel:kernel /tmp/runtime-kernel || true

echo "[pulse] Starting daemon"
exec runuser -u kernel -- env \
  XDG_RUNTIME_DIR=/tmp/runtime-kernel \
  XDG_CONFIG_HOME=/home/kernel/.config \
  XDG_CACHE_HOME=/home/kernel/.cache \
  pulseaudio --log-level=error \
             --disallow-module-loading \
             --disallow-exit \
             --exit-idle-time=-1

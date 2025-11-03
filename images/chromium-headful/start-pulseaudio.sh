#!/bin/bash

set -o pipefail -o errexit -o nounset

if [[ "${RUN_AS_ROOT:-false}" == "true" ]]; then
  echo "Not starting PulseAudio daemon when running as root"
  exit 0
else
  # Set up permissions before starting PulseAudio
  chown -R kernel:kernel /home/kernel/ /home/kernel/.config /etc/pulse 2>/dev/null || true
  chmod 777 /home/kernel/.config /etc/pulse 2>/dev/null || true
  chown -R kernel:kernel /tmp/runtime-kernel 2>/dev/null || true

  # Start PulseAudio as kernel user using config files
  exec runuser -u kernel -- env \
    XDG_RUNTIME_DIR=/tmp/runtime-kernel \
    XDG_CONFIG_HOME=/home/kernel/.config \
    XDG_CACHE_HOME=/home/kernel/.cache \
    pulseaudio --log-level=error \
               --disallow-module-loading \
               --disallow-exit \
               --exit-idle-time=-1
fi

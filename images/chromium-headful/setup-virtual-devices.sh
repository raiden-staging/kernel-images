#!/bin/bash

set -o pipefail -o errexit -o nounset

echo "[virtual-devices] Setting up virtual video and audio input devices"

# Load v4l2loopback module to create virtual video device
# video_nr=10 creates /dev/video10
# card_label sets the device name that Chrome will see
# exclusive_caps=1 makes the device behave more like a real camera
if ! lsmod | grep -q v4l2loopback; then
  echo "[virtual-devices] Loading v4l2loopback kernel module"
  modprobe v4l2loopback video_nr=10 card_label="Virtual Camera" exclusive_caps=1 || {
    echo "[virtual-devices] Warning: Failed to load v4l2loopback module"
    echo "[virtual-devices] This is expected when running in environments without kernel module support"
    echo "[virtual-devices] Virtual camera will not be available"
  }
else
  echo "[virtual-devices] v4l2loopback module already loaded"
fi

# Verify the virtual video device was created
if [ -e /dev/video10 ]; then
  echo "[virtual-devices] Virtual camera device created at /dev/video10"
  chmod 666 /dev/video10
  v4l2-ctl --device=/dev/video10 --list-formats-ext || true
else
  echo "[virtual-devices] Warning: /dev/video10 not found - virtual camera unavailable"
fi

# The virtual microphone is already set up via PulseAudio configuration
# (see default.pa: module-virtual-source source_name=microphone)
echo "[virtual-devices] Virtual microphone available via PulseAudio (device: microphone)"

echo "[virtual-devices] Virtual device setup complete"

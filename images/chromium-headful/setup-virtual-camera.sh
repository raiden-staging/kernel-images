#!/bin/bash

set -o pipefail -o nounset

# Setup virtual camera device using v4l2loopback
# Note: This requires the container to run with --privileged flag or appropriate capabilities
# to load kernel modules. If running without privileges, this step will be skipped.

echo "[virtual-camera] Setting up virtual camera device"

# Check if we can load kernel modules
if [ -w /dev ]; then
  # Try to load v4l2loopback module
  if modprobe v4l2loopback devices=1 video_nr=20 card_label="Virtual Camera" exclusive_caps=1 2>/dev/null; then
    echo "[virtual-camera] Successfully loaded v4l2loopback module"
    echo "[virtual-camera] Virtual camera device created at /dev/video20"
  else
    echo "[virtual-camera] Warning: Could not load v4l2loopback module"
    echo "[virtual-camera] Container may need --privileged flag or --cap-add=SYS_MODULE"
    echo "[virtual-camera] Virtual camera features will not be available"
  fi
else
  echo "[virtual-camera] Warning: Cannot write to /dev, skipping v4l2loopback setup"
  echo "[virtual-camera] Virtual camera features will not be available"
fi

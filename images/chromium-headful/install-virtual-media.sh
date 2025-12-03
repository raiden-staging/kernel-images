#!/usr/bin/env bash

set -o errexit -o pipefail -o nounset

HOST_ARCH="$(dpkg --print-architecture)"
ARCH="${TARGETARCH:-$HOST_ARCH}"

# When cross-building (TARGETARCH differs from the host architecture), skip installing
# kernel modules because they won't match the running kernel.
if [[ -n "${TARGETARCH:-}" && "$TARGETARCH" != "$HOST_ARCH" ]]; then
  echo "[virtual-media] Skipping v4l2loopback install for cross-build (${TARGETARCH} on ${HOST_ARCH})"
  exit 0
fi

case "$ARCH" in
  amd64|arm64) ;;
  *)
    echo "[virtual-media] Skipping v4l2loopback install for unsupported architecture ${ARCH}"
    exit 0
    ;;
esac

kernel_ver="$(uname -r)"
echo "[virtual-media] Preparing v4l2loopback for kernel ${kernel_ver}"

export DEBIAN_FRONTEND=noninteractive

install_v4l2loopback() {
  apt-get update
  apt-get --no-install-recommends -y install ca-certificates curl dkms kmod

  local tried=()
  local headers_candidates=(
    "linux-headers-${kernel_ver}"
    "linux-modules-extra-${kernel_ver}"
    "linux-headers-generic"
    "linux-headers-virtual"
    "linux-headers-generic-hwe-22.04"
  )

  for pkg in "${headers_candidates[@]}"; do
    tried+=("$pkg")
    if apt-get --no-install-recommends -y install "$pkg"; then
      echo "[virtual-media] Installed kernel media support via ${pkg}"
      break
    fi
  done

  apt-get --no-install-recommends -y install v4l2loopback-dkms v4l2loopback-utils v4l-utils
  DKMS_FORCE=1 dkms autoinstall -k "${kernel_ver}" || true
  depmod "${kernel_ver}" || true
}

install_v4l2loopback

if modinfo v4l2loopback >/dev/null 2>&1; then
  echo "[virtual-media] v4l2loopback module is available"
else
  echo "[virtual-media] v4l2loopback module is still unavailable; host kernel may need media support" >&2
fi

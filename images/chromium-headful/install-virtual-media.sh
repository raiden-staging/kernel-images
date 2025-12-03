#!/usr/bin/env bash

set -o errexit -o pipefail -o nounset

ARCH="${TARGETARCH:-$(dpkg --print-architecture)}"
if [[ "$ARCH" != "amd64" ]]; then
  echo "[virtual-media] Skipping v4l2loopback install for architecture ${ARCH}"
  exit 0
fi

kernel_ver="$(uname -r)"
echo "[virtual-media] Preparing v4l2loopback for kernel ${kernel_ver}"

export DEBIAN_FRONTEND=noninteractive

install_debian_keyring() {
  local keyring_pkg="debian-archive-keyring_2023.3+deb12u2_all.deb"
  if [[ -s /usr/share/keyrings/debian-archive-keyring.gpg ]]; then
    return
  fi
  echo "[virtual-media] Installing Debian archive keyring"
  curl -fsSL "http://deb.debian.org/debian/pool/main/d/debian-archive-keyring/${keyring_pkg}" -o "/tmp/${keyring_pkg}"
  dpkg -i "/tmp/${keyring_pkg}"
  install -m644 /usr/share/keyrings/debian-archive-keyring.gpg /etc/apt/trusted.gpg.d/debian-archive-keyring.gpg
  rm -f "/tmp/${keyring_pkg}"
}

add_bookworm_sources() {
  install_debian_keyring
  cat > /etc/apt/sources.list.d/debian-bookworm.list <<'EOF'
deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian bookworm main contrib non-free-firmware
deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian-security bookworm-security main contrib non-free-firmware
deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian bookworm-updates main contrib non-free-firmware
EOF
}

cleanup_bookworm_sources() {
  rm -f /etc/apt/sources.list.d/debian-bookworm.list
}

install_kernel_media_support() {
  local kernel_ver="$1"
  local tried=()
  local packages=(
    "linux-headers-${kernel_ver}"
    "linux-modules-extra-${kernel_ver}"
    "linux-modules-${kernel_ver}"
    "linux-modules-extra-cloud-amd64"
  )

  for pkg in "${packages[@]}"; do
    tried+=("$pkg")
    if apt-get --no-install-recommends -y install "$pkg"; then
      echo "[virtual-media] Installed ${pkg}"
      return 0
    fi
  done

  echo "[virtual-media] Unable to install kernel media support packages (tried: ${tried[*]})"
  return 1
}

install_media_stack_from_source() {
  local kernel_ver="$1"
  local workdir="/tmp/media-build"

  echo "[virtual-media] Attempting media_build fallback for kernel ${kernel_ver}"
  apt-get --no-install-recommends -y install patchutils libproc-processtable-perl git

  rm -rf "$workdir"
  if ! git clone --depth=1 --branch for-v5.18 --single-branch https://git.linuxtv.org/media_build.git "$workdir"; then
    echo "[virtual-media] Failed to clone media_build" >&2
    return 1
  fi

  if ! (cd "$workdir" && ./build --depth 1 && make install); then
    echo "[virtual-media] media_build compilation failed" >&2
    return 1
  fi

  depmod "${kernel_ver}" || true
  return 0
}

install_v4l2loopback() {
  add_bookworm_sources
  apt-get update
  apt-get --no-install-recommends -y install ca-certificates curl dkms kmod

  install_kernel_media_support "$kernel_ver" || true

  apt-get --no-install-recommends -y install v4l2loopback-dkms v4l2loopback-utils v4l-utils
  DKMS_FORCE=1 dkms autoinstall -k "${kernel_ver}" || true
  depmod "${kernel_ver}" || true

  if ! modinfo v4l2loopback >/dev/null 2>&1; then
    install_media_stack_from_source "$kernel_ver" || true
    DKMS_FORCE=1 dkms autoinstall -k "${kernel_ver}" || true
    depmod "${kernel_ver}" || true
  fi

  cleanup_bookworm_sources
}

install_v4l2loopback

if modinfo v4l2loopback >/dev/null 2>&1; then
  echo "[virtual-media] v4l2loopback module is available"
else
  echo "[virtual-media] v4l2loopback module is still unavailable; host kernel may need media support" >&2
fi

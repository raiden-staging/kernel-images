# webrtc client
FROM node:22-bullseye-slim AS client
WORKDIR /src
COPY client/package*.json ./
RUN npm install
COPY client/ .
RUN npm run build

# xorg dependencies
FROM docker.io/ubuntu:22.04 AS xorg-deps
WORKDIR /xorg
ENV DEBIAN_FRONTEND=noninteractive
RUN set -eux; \
    apt-get update; \
    apt-get install -y \
    git gcc pkgconf autoconf automake libtool make xorg-dev xutils-dev \
    && rm -rf /var/lib/apt/lists/*;
COPY xorg-deps/ /xorg/
# build xf86-video-dummy v0.3.8 with RandR support
RUN set -eux; \
    cd xf86-video-dummy/v0.3.8; \
    patch -p1 < ../01_v0.3.8_xdummy-randr.patch; \
    autoreconf -v --install; \
    ./configure; \
    make -j$(nproc); \
    make install;
# build custom input driver
RUN set -eux; \
    cd xf86-input-neko; \
    ./autogen.sh --prefix=/usr; \
    ./configure; \
    make -j$(nproc); \
    make install;

FROM ghcr.io/onkernel/neko/base:3.0.6-v1.0.1 AS neko
FROM docker.io/ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive
ENV DEBIAN_PRIORITY=high

RUN apt-get update && \
    apt-get -y upgrade && \
    apt-get -y install \
    # UI Requirements
    xvfb \
    xterm \
    xdotool \
    scrot \
    imagemagick \
    sudo \
    mutter \
    x11vnc \
    upower \
    # Python/pyenv reqs
    build-essential \
    libssl-dev  \
    zlib1g-dev \
    libbz2-dev \
    libreadline-dev \
    libsqlite3-dev \
    curl \
    git \
    libncursesw5-dev \
    xz-utils \
    tk-dev \
    libxml2-dev \
    libxmlsec1-dev \
    libffi-dev \
    liblzma-dev \
    # Network tools
    net-tools \
    netcat \
    # PPA req
    software-properties-common && \
    # Userland apps
    sudo add-apt-repository ppa:mozillateam/ppa && \
    sudo apt-get install -y --no-install-recommends \
    chromium-browser \
    libreoffice \
    x11-apps \
    xpdf \
    gedit \
    xpaint \
    tint2 \
    galculator \
    pcmanfm \
    wget \
    xdg-utils \
    libvulkan1 \
    fonts-liberation \
    unzip && \
    apt-get clean

# install ffmpeg manually since the version available in apt is from the 4.x branch due to #drama.
# as of writing these static builds will be the latest 7.0.x release.
RUN set -eux; \
    URL="https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz"; \
    echo "Downloading FFmpeg static build from $URL"; \
    curl -fsSL "$URL" -o /tmp/ffmpeg.tar.xz; \
    tar -xJf /tmp/ffmpeg.tar.xz -C /tmp; \
    install -m755 /tmp/ffmpeg-*/ffmpeg  /usr/local/bin/ffmpeg; \
    install -m755 /tmp/ffmpeg-*/ffprobe /usr/local/bin/ffprobe; \
    rm -rf /tmp/ffmpeg*

 # runtime
ARG KERNEL_USER=kernel
ARG KERNEL_UID=1000
ARG KERNEL_GID=$KERNEL_UID
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        wget ca-certificates python2 supervisor xclip xdotool \
        pulseaudio dbus-x11 xserver-xorg-video-dummy \
        libcairo2 libxcb1 libxrandr2 libxv1 libopus0 libvpx7 \
        gstreamer1.0-plugins-base gstreamer1.0-plugins-good \
        gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly \
        gstreamer1.0-pulseaudio gstreamer1.0-omx; \
    #
    # install libxcvt0 (not available in debian:bullseye)
    ARCH=$(dpkg --print-architecture); \
    wget http://ftp.de.debian.org/debian/pool/main/libx/libxcvt/libxcvt0_0.1.2-1_${ARCH}.deb; \
    apt-get install --no-install-recommends ./libxcvt0_0.1.2-1_${ARCH}.deb; \
    rm ./libxcvt0_0.1.2-1_${ARCH}.deb; \
    #
    # create a non-root user and add to all relevant audio groups
    groupadd --gid $KERNEL_GID $KERNEL_USER; \
    useradd --uid $KERNEL_UID --gid $KERNEL_GID --shell /bin/bash --create-home $KERNEL_USER; \
    adduser $KERNEL_USER audio; \
    adduser $KERNEL_USER video; \
    adduser $KERNEL_USER pulse; \
    adduser $KERNEL_USER pulse-access; \
    #
    # workaround for an X11 problem: http://blog.tigerteufel.de/?p=476
    mkdir /tmp/.X11-unix; \
    chmod 1777 /tmp/.X11-unix; \
    chown $KERNEL_USER:$KERNEL_USER /tmp/.X11-unix/; \
    #
    # make directories for neko
    mkdir -p /etc/neko /var/www /var/log/neko \
        /tmp/runtime-$KERNEL_USER \
        /home/$KERNEL_USER/.config/pulse  \
        /home/$KERNEL_USER/.local/share/xorg; \
    chmod 1777 /var/log/neko; \
    chown $KERNEL_USER:$KERNEL_USER /var/log/neko/ /tmp/runtime-$KERNEL_USER; \
    chown -R $KERNEL_USER:$KERNEL_USER /home/$KERNEL_USER; \
    chown -R $KERNEL_USER:$KERNEL_USER /home/$KERNEL_USER/.config/pulse /etc/pulse || true; \
    # Ensure /home/$KERNEL_USER/.config and /home/$KERNEL_USER/.config/pulse are owned by the user and world-writable
    chmod 777 /home/$KERNEL_USER/.config || true; \
    chmod 777 /home/$KERNEL_USER/.config/pulse || true; \
    # Also ensure /etc/pulse is world-writable for config file access
    chmod 777 /etc/pulse || true; \
    # clean up
    apt-get clean -y; \
    rm -rf /var/lib/apt/lists/* /var/cache/apt/

# install chromium & ncat for proxying the remote debugging port
RUN add-apt-repository -y ppa:xtradeb/apps
RUN apt update -y && apt install -y chromium ncat

# Install noVNC
RUN git clone --branch v1.5.0 https://github.com/novnc/noVNC.git /opt/noVNC && \
    git clone --branch v0.12.0 https://github.com/novnc/websockify /opt/noVNC/utils/websockify && \
    ln -s /opt/noVNC/vnc.html /opt/noVNC/index.html

# setup desktop env & app
ENV DISPLAY_NUM=1
ENV HEIGHT=768
ENV WIDTH=1024
ENV WITHDOCKER=true

COPY xorg.conf /etc/neko/xorg.conf

COPY default.pa /etc/pulse/default.pa
COPY default.pa /home/kernel/.config/pulse/default.pa

COPY daemon.conf /etc/pulse/daemon.conf
COPY daemon.conf /home/kernel/.config/pulse/daemon.conf
COPY daemon.conf /home/kernel/.config/pulse//daemon.conf

COPY dbus-pulseaudio.conf /etc/dbus-1/system.d/pulseaudio.conf
COPY dbus-mpris.conf /etc/dbus-1/system.d/mpris.conf
COPY neko.yaml /etc/neko/neko.yaml
COPY --from=neko /usr/bin/neko /usr/bin/neko
COPY --from=client /src/dist/ /var/www
COPY --from=xorg-deps /usr/local/lib/xorg/modules/drivers/dummy_drv.so /usr/lib/xorg/modules/drivers/dummy_drv.so
COPY --from=xorg-deps /usr/local/lib/xorg/modules/input/neko_drv.so /usr/lib/xorg/modules/input/neko_drv.so

COPY image-chromium/ /
COPY ./wrapper.sh /wrapper.sh

# copy the kernel-images API binary built externally
COPY bin/kernel-images-api /usr/local/bin/kernel-images-api
ENV WITH_KERNEL_IMAGES_API=false

ENV USER=kernel
ENV XDG_RUNTIME_DIR=/tmp/runtime-kernel
ENV PULSE_SERVER=unix:${XDG_RUNTIME_DIR}/pulse/native

ENV XDG_CONFIG_HOME=/tmp/.chromium
ENV XDG_CACHE_HOME=/tmp/.chromium

ENTRYPOINT [ "/wrapper.sh" ]
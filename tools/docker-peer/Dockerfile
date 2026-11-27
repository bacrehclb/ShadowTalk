# syntax=docker/dockerfile:1
# Manual Linux peer for testing the packaged release against the macOS app.
FROM debian:trixie-slim

ARG TARGETARCH
ARG SHADOWTALK_VERSION=0.1.0-alpha.2

# The caller supplies the verified release directory as a named build context.
COPY --from=release /shadowtalk_${SHADOWTALK_VERSION}_linux_${TARGETARCH}.deb /tmp/packages/
COPY --from=release /checksums.txt /tmp/packages/checksums.txt
RUN set -eu; \
    cd /tmp/packages; \
    package="shadowtalk_${SHADOWTALK_VERSION}_linux_${TARGETARCH}.deb"; \
    awk -v name="$package" '$2 == name {print; count++} END {if (count != 1) exit 1}' checksums.txt > selected.sha256; \
    sha256sum -c selected.sha256; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates dbus-daemon dbus-bin gnome-keyring libglib2.0-bin \
        "./$package"; \
    rm -rf /var/lib/apt/lists/* /tmp/packages; \
    useradd --create-home --uid 1000 --shell /bin/bash peer; \
    install -d -o peer -g peer -m 0700 /home/peer/.config /home/peer/.local/share

COPY --chmod=0755 session.sh /usr/local/bin/shadowtalk-peer-session
USER peer
WORKDIR /home/peer
ENV LANG=C.UTF-8 TERM=xterm-256color \
    XDG_CONFIG_HOME=/home/peer/.config \
    XDG_DATA_HOME=/home/peer/.local/share \
    XDG_RUNTIME_DIR=/tmp/shadowtalk-peer-runtime

ENTRYPOINT ["/usr/local/bin/shadowtalk-peer-session"]

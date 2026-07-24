# Production image for the Mosaic Platform.
#
# This is a *packaging* artefact, not a build. It copies the binary the release
# workflow already cross-compiled and does not compile anything itself, so a
# container deployment and a bare-metal one run the identical bytes — the "same
# binary, different topology" property ADR 0080 turns on. The token injection,
# the CGO-off cross-compile and the trimpath all happened in the build job; this
# only wraps the result.
#
# It bundles ffmpeg because the Platform shells out to ffprobe at runtime to
# decide what a release is and to ffmpeg to re-encode streams a client cannot
# decode (ADR 0050). Without them the Platform relays unprobed and a release
# with undecodable audio plays silently — so a container that omitted them would
# be a subtly broken Mosaic. A bare-metal user installs ffmpeg themselves; this
# is the one runtime dependency the image papers over and the native install
# must not forget.
#
# debian-slim rather than distroless or scratch precisely because of ffmpeg: a
# scratch image cannot carry it, and the saving over slim is not worth shipping a
# Mosaic that cannot probe.
FROM debian:bookworm-slim

# TARGETARCH is set per platform by buildx, so one Dockerfile produces both the
# amd64 and arm64 image from the matching pre-built binary.
ARG TARGETARCH

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ffmpeg ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    ffprobe -version | head -n 1

# A non-root user: the Platform needs no privilege to serve, and running as root
# in a container is a default worth not taking. The extension-module egress
# controls (ADR 0064's layer 3) are a separate, stronger boundary; this is the
# ordinary hygiene beneath them.
RUN useradd --system --uid 10001 --home /var/lib/mosaic --create-home mosaic

# The binary arrives executable from `go build` and COPY preserves its mode, so
# no chmod is needed — and avoiding COPY --chmod keeps the image buildable with a
# plain `docker build`, not only under BuildKit.
COPY dist/mosaic-platform-linux-${TARGETARCH} /usr/local/bin/mosaic-platform

USER mosaic
WORKDIR /var/lib/mosaic

# 8080 is the Supervisor handoff (readiness, liveness); 8081 the client API,
# artwork and playback (ADR 0061, the dev stack's own mapping).
EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/mosaic-platform"]

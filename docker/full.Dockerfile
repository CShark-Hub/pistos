# Complete image: a pack image plus the pistos core and git, so CI can run `pistos review` directly
# in the image (no Docker-in-Docker, no mounted Docker socket). Build the pack image first with
# `pistos plugins install` (execution.mode = "docker"), then:
#   docker build -f docker/full.Dockerfile --build-arg PACK=go -t pistos-go:dev dist/linux-arm64
# where the build context contains the `pistos` binary for the image's platform.
ARG PACK=go
ARG PACK_VERSION=0.1.0
FROM pistos-pack-${PACK}:${PACK_VERSION}
USER root
RUN if command -v apt-get >/dev/null; then apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*; fi
COPY pistos /usr/local/bin/pistos
ENV PISTOS_IN_IMAGE=1 PISTOS_CACHE_DIR=/tmp/pistos-cache
ENTRYPOINT ["/usr/local/bin/pistos"]

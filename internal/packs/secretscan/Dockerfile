# Secrets pack image: gitleaks only. The official golang image is used only to build it; gitleaks is a
# static Go binary, so the final stage is slim Debian without any language runtime. The version is
# stamped through ldflags because plain `go install` builds report "version is set by build process".
FROM golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS build
RUN CGO_ENABLED=0 GOBIN=/out GOTOOLCHAIN=local go install \
    -ldflags "-X github.com/zricethezav/gitleaks/v8/version.Version=8.30.1" \
    github.com/zricethezav/gitleaks/v8@v8.30.1

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
COPY --from=build /out/gitleaks /usr/local/bin/gitleaks
ENV PATH=/usr/local/bin:/usr/bin:/bin

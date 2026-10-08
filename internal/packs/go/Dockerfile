# Go pack image: Go toolchain, Staticcheck and gopls only. Built by `pistos plugins install` when
# execution.mode = "docker". The official golang image is used only to build the tools; the final
# stage is slim Debian so the image carries no unrelated runtimes (it would otherwise ship Python).
FROM golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS build
RUN CGO_ENABLED=0 GOBIN=/out GOTOOLCHAIN=local go install honnef.co/go/tools/cmd/staticcheck@v0.8.1 \
    && CGO_ENABLED=0 GOBIN=/out GOTOOLCHAIN=local go install golang.org/x/tools/gopls@v0.23.0

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
COPY --from=build /usr/local/go /usr/local/go
COPY --from=build /out/ /usr/local/bin/
# No C toolchain is installed, so cgo is off; packages that require cgo are reported by go vet.
ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin GOTOOLCHAIN=local CGO_ENABLED=0

# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26
ARG DEBIAN_VERSION=trixie

FROM golang:${GO_VERSION}-${DEBIAN_VERSION} AS go

# Debian's ImageMagick 7 ships with liquid rescale (liblqr) support.
FROM debian:${DEBIAN_VERSION}-slim AS tools
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg imagemagick ca-certificates tini \
 && rm -rf /var/lib/apt/lists/*

FROM --platform=$BUILDPLATFORM go AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/distortioner ./cmd/distortioner

# Integration tests run against the exact tool versions of the runtime image.
FROM tools AS itest
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["go", "test", "-tags", "integration", "-count", "1", "./internal/media/..."]

FROM tools AS runtime
RUN useradd --system --uid 10001 --home-dir /app distortioner \
 && mkdir -p /app/data && chown distortioner /app/data
WORKDIR /app
COPY --from=build /out/distortioner /usr/local/bin/distortioner
USER distortioner
ENV DISTORTIONER_DATA_DIR=/app/data \
    DISTORTIONER_LISTEN_ADDR=:8080
VOLUME /app/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["distortioner", "healthcheck"]
ENTRYPOINT ["tini", "--", "distortioner"]

# syntax=docker/dockerfile:1
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY cmd/ cmd/
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/room-pass ./cmd/room-pass
FROM gcr.io/distroless/static-debian12:nonroot
# Accepted so the build args the root Taskfile passes to every image land as OCI
# metadata here too, the same way frontend/Dockerfile records them.
ARG VERSION
ARG GIT_COMMIT
ARG BUILD_DATE
LABEL org.opencontainers.image.version=${VERSION} \
      org.opencontainers.image.revision=${GIT_COMMIT} \
      org.opencontainers.image.created=${BUILD_DATE}
COPY --from=build /out/room-pass /room-pass
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/room-pass"]

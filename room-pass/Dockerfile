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
COPY --from=build /out/room-pass /room-pass
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/room-pass"]

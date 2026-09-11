# syntax=docker/dockerfile:1
#
# One image for the whole demo: the Vue bundle and the Go backend that serves
# it. The build context is the REPOSITORY ROOT, not this directory, because the
# frontend sources live in ../frontend. Build it with:
#
#   task image-voter
#   docker buildx build -f voter/Dockerfile .

############################################
# Frontend bundle
############################################
FROM node:22-alpine AS frontend
WORKDIR /app

ARG GIT_COMMIT
ARG GIT_DIRTY
ARG BUILD_DATE
ENV VITE_GIT_COMMIT=${GIT_COMMIT}
ENV VITE_GIT_DIRTY=${GIT_DIRTY}
ENV VITE_BUILD_DATE=${BUILD_DATE}

# Dependencies first, so editing a .vue file does not reinstall node_modules.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

############################################
# Backend binary
############################################
# krm-stream 0.3.0 requires Go 1.27.1.
FROM golang:1.27.1-bookworm AS build
WORKDIR /src

COPY voter/go.mod voter/go.sum ./
RUN go mod download

COPY voter/*.go ./

ARG GIT_COMMIT
ARG GIT_DIRTY
ARG BUILD_DATE
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-X main.gitCommit=${GIT_COMMIT} -X main.gitDirty=${GIT_DIRTY} -X main.buildDate=${BUILD_DATE}" -o /out/voter .

############################################
# Runtime
############################################
FROM debian:bookworm-slim
ARG GIT_COMMIT
ARG BUILD_DATE
LABEL org.opencontainers.image.revision=${GIT_COMMIT} \
      org.opencontainers.image.created=${BUILD_DATE}

# ca-certificates: the OIDC client fetches Dex discovery and the JWKS over TLS.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/voter /voter
COPY --from=frontend /app/dist /srv/www

EXPOSE 8080
ENV HOST=0.0.0.0
ENV PORT=8080
ENV STATIC_DIR=/srv/www

# Nothing is written to disk at runtime, so this process has no reason to be
# root. 65532 matches the runAsUser the deployment pins (the distroless nonroot
# convention); a numeric id needs no /etc/passwd entry. The binary and the
# bundle are world-readable, so any uid can serve them.
USER 65532:65532
ENTRYPOINT ["/voter"]

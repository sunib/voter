# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go *.tmpl ./

ARG GIT_COMMIT
ARG GIT_DIRTY
ARG BUILD_DATE
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-X main.gitCommit=${GIT_COMMIT} -X main.gitDirty=${GIT_DIRTY} -X main.buildDate=${BUILD_DATE}" -o /out/auth-service .

FROM debian:bookworm-slim
ARG GIT_COMMIT
ARG BUILD_DATE
LABEL org.opencontainers.image.revision=${GIT_COMMIT} \
      org.opencontainers.image.created=${BUILD_DATE}
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/auth-service /auth-service
EXPOSE 8080
ENV HOST=0.0.0.0
ENV PORT=8080
ENTRYPOINT ["/auth-service"]

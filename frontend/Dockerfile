# syntax=docker/dockerfile:1

############################################
# Build stage
############################################
FROM node:22-alpine AS build

WORKDIR /app

ARG GIT_COMMIT
ARG GIT_DIRTY
ARG BUILD_DATE
ENV VITE_GIT_COMMIT=${GIT_COMMIT}
ENV VITE_GIT_DIRTY=${GIT_DIRTY}
ENV VITE_BUILD_DATE=${BUILD_DATE}

# Install deps first (better layer caching)
COPY package.json package-lock.json ./
RUN npm ci

# Copy the rest of the source and build
COPY . .
RUN npm run build

############################################
# Runtime stage (non-root NGINX, good default for Kubernetes)
############################################
FROM nginxinc/nginx-unprivileged:stable-alpine AS runtime
ARG GIT_COMMIT
ARG BUILD_DATE
LABEL org.opencontainers.image.revision=${GIT_COMMIT} \
      org.opencontainers.image.created=${BUILD_DATE}

# Replace the default site config (SPA-friendly)
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Copy static assets
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 8080

# Basic container healthcheck (Kubernetes can also do HTTP probes)
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1

# Single cmd build: 
# docker buildx build --push -t zot.z65.nl/present/frontend:latest .

#!/usr/bin/env bash
set -euo pipefail
# Remove only the fixture's explicitly named cluster and its owned Docker resources.
k3d cluster delete room-pass-e2e
if [ -f /.dockerenv ]; then
  docker network disconnect k3d-room-pass-e2e "$(hostname)" 2>/dev/null || true
fi
if docker network inspect k3d-room-pass-e2e >/dev/null 2>&1; then
  docker network rm k3d-room-pass-e2e
fi
docker volume rm room-pass-e2e-config

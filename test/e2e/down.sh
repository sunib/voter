#!/usr/bin/env bash
set -euo pipefail
# Remove only the fixture's explicitly named cluster and its owned Docker resources.
k3d cluster delete voter-e2e
if [ -f /.dockerenv ]; then
  docker network disconnect k3d-voter-e2e "$(hostname)" 2>/dev/null || true
fi
if docker network inspect k3d-voter-e2e >/dev/null 2>&1; then
  docker network rm k3d-voter-e2e
fi
docker volume rm voter-e2e-config

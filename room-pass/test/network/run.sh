#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
# This suite owns its cluster. It never reads the user's kubeconfig or reuses a
# running cluster: even policy-removal controls stay confined to this fixture.
cluster=dex-network-e2e
clusters=$(k3d cluster list -o json)
if jq -e 'any(.[]; .name == "dex-network-e2e")' <<< "$clusters" >/dev/null; then
  echo "Cluster $cluster already exists; remove it explicitly before running this suite." >&2
  exit 1
fi
mkdir -p .local
cleanup() {
  k3d cluster delete "$cluster"
  rm -f .local/network-kubeconfig
}
trap cleanup EXIT
k3d cluster create "$cluster" --image rancher/k3s:v1.31.5-k3s1 --servers 1 --agents 1 --wait \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false \
  --k3s-arg '--disable=traefik@server:0'
k3d kubeconfig get "$cluster" > .local/network-kubeconfig
chmod 600 .local/network-kubeconfig
if [ -f /.dockerenv ]; then
  docker network connect "k3d-$cluster" "$(hostname)"
  trap 'docker network disconnect "k3d-$cluster" "$(hostname)" || true; cleanup' EXIT
  server_ip=$(docker inspect "k3d-$cluster-server-0" --format '{{(index .NetworkSettings.Networks "k3d-dex-network-e2e").IPAddress}}')
  sed -i "s|server: .*|server: https://$server_ip:6443|" .local/network-kubeconfig
fi
ROOM_PASS_NETWORK_E2E=1 go test -v -count=1 -timeout=12m ./test/network

#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
mkdir -p .local
chmod 700 .local
# Dedicated cluster and kubeconfig; never switch the user's current context.
if ! k3d cluster list -o json | python3 -c 'import json,sys;sys.exit(not any(c["name"]=="room-pass-e2e" for c in json.load(sys.stdin)))'; then
  openssl req -x509 -newkey rsa:2048 -nodes -keyout .local/tls.key -out .local/tls.crt -days 7 -subj /CN=roompass-test -addext 'subjectAltName=DNS:demo.roompass.test,DNS:login.roompass.test,DNS:app.roompass.test' >/dev/null 2>&1
  # A Docker volume works with a sibling Docker daemon (host paths need not match).
  docker volume create room-pass-e2e-config >/dev/null
  docker run --rm -i -v room-pass-e2e-config:/config alpine:3.21 sh -c 'cat > /config/ca.crt' < .local/tls.crt
  docker run --rm -i -v room-pass-e2e-config:/config alpine:3.21 sh -c 'cat > /config/audit-policy.yaml' < test/e2e/audit-policy.yaml
  python3 -c 'from pathlib import Path; p=Path("test/e2e/authentication-config.yaml").read_text(); ca=Path(".local/tls.crt").read_text(); Path(".local/authentication-config.yaml").write_text(p.replace("CA_PEM", "\n".join("        "+line for line in ca.splitlines())))'
  # Parse the RENDERED config before handing it to the apiserver. A malformed
  # authenticator does not fail loudly: k3s exits, k3d keeps waiting for an API
  # that will never answer, and the whole thing looks like a slow cluster. That
  # cost a 30-minute CI timeout on 2026-09-10 -- an unquoted `message:` value
  # containing ": " parsed as a nested mapping. Two seconds here, instead.
  python3 -c 'import sys,yaml; yaml.safe_load(open(".local/authentication-config.yaml"))' || {
    echo "ERROR: .local/authentication-config.yaml is not valid YAML." >&2
    echo "       The apiserver would fail to start and the bringup would hang." >&2
    exit 1
  }
  docker run --rm -i -v room-pass-e2e-config:/config alpine:3.21 sh -c 'cat > /config/authentication-config.yaml' < .local/authentication-config.yaml
  docker network inspect k3d-room-pass-e2e >/dev/null 2>&1 || docker network create k3d-room-pass-e2e >/dev/null
  gateway=$(docker network inspect k3d-room-pass-e2e --format '{{(index .IPAM.Config 0).Gateway}}')
  # --timeout bounds the --wait. Without it a server that never becomes ready
  # blocks until the CI job's own timeout kills it, which reports as "tests
  # were cancelled" rather than "the cluster did not come up".
  k3d cluster create room-pass-e2e --image rancher/k3s:v1.31.5-k3s1 --servers 1 --agents 0 --wait --timeout 180s --network k3d-room-pass-e2e --host-alias "$gateway:login.roompass.test" --host-alias "$gateway:app.roompass.test" \
    --kubeconfig-update-default=false --kubeconfig-switch-context=false \
    --port '18443:443@server:0' \
    --volume 'room-pass-e2e-config:/etc/room-pass@server:0' \
    --k3s-arg '--kube-apiserver-arg=audit-policy-file=/etc/room-pass/audit-policy.yaml@server:0' \
    --k3s-arg '--kube-apiserver-arg=audit-log-path=/etc/room-pass/audit.log@server:0' \
    --k3s-arg '--kube-apiserver-arg=audit-log-maxsize=20@server:0' \
    --k3s-arg '--kube-apiserver-arg=authentication-config=/etc/room-pass/authentication-config.yaml@server:0'
fi
k3d kubeconfig get room-pass-e2e > .local/kubeconfig
chmod 600 .local/kubeconfig
# Resolve issuer inside the API-server container through the host's published TLS port.
gateway=$(docker network inspect k3d-room-pass-e2e --format '{{(index .IPAM.Config 0).Gateway}}')
# The devcontainer must be able to reach the cluster API's advertised address.
docker network connect k3d-room-pass-e2e "$(hostname)" 2>/dev/null || true
server_ip=$(docker inspect k3d-room-pass-e2e-server-0 --format '{{(index .NetworkSettings.Networks "k3d-room-pass-e2e").IPAddress}}')
if [ -f /.dockerenv ]; then
  sed -i "s|server: .*|server: https://$server_ip:6443|" .local/kubeconfig
fi
export KUBECONFIG="$PWD/.local/kubeconfig"
kubectl wait --for=condition=Ready node/k3d-room-pass-e2e-server-0 --timeout=90s
kubectl apply -f config/crd
kubectl apply -k deploy/base
if ! kubectl -n room-pass get secret room-pass-cookie >/dev/null 2>&1; then
  openssl rand 32 > .local/hash-key
  openssl rand 32 > .local/block-key
  kubectl -n room-pass create secret generic room-pass-cookie --from-file=hash-key=.local/hash-key --from-file=block-key=.local/block-key
fi
kubectl -n room-pass create secret tls local-tls --cert=.local/tls.crt --key=.local/tls.key --dry-run=client -o yaml | kubectl apply -f -
ends_at=$(date -u -d '+4 hours' +%Y-%m-%dT%H:%M:%SZ)
cat <<YAML | kubectl apply -f -
apiVersion: roompass.configbutler.ai/v1alpha1
kind: Room
metadata:
  name: demo
  namespace: room-pass
spec:
  title: Room Pass local demo
  endsAt: "$ends_at"
  enrollment: Open
  maxParticipants: 300
  audienceGroup: demo:room-pass-test
  allowedReturnURLs: [https://demo.roompass.test:18443/app/, 'https://app.roompass.test:18443/']
YAML
docker build -t room-pass:dev .
k3d image import room-pass:dev -c room-pass-e2e
# Helm installs Traefik CRDs asynchronously during k3s bootstrap.
for attempt in $(seq 1 45); do
  if kubectl get crd middlewares.traefik.io >/dev/null 2>&1; then break; fi
  sleep 2
done
kubectl wait --for=condition=Established crd/middlewares.traefik.io --timeout=30s
kubectl apply -f test/e2e/dex.yaml -f test/e2e/edge.yaml
kubectl -n room-pass rollout restart deployment/dex
kubectl -n room-pass rollout restart deployment/room-pass
kubectl -n room-pass rollout status deployment/room-pass --timeout=180s
kubectl -n room-pass rollout status deployment/dex --timeout=180s
printf 'Cluster ready. Kubeconfig: %s/.local/kubeconfig\n' "$PWD"

# Browser client resolves the public issuer through the isolated Docker gateway.
kubectl -n room-pass create configmap issuer-ca --from-file=ca.crt=.local/tls.crt --dry-run=client -o yaml | kubectl apply -f -

# The real application, so the fixture can exercise the Voter session and its
# live stream and not only the minimal demo client. Built from the repository
# root because the image contains both the Go backend and the Vue bundle.
docker build -t voter:dev -f ../voter/Dockerfile ..
k3d image import voter:dev -c room-pass-e2e
# The CRD first, and awaited: applying it together with a CoffeeConfig loses the
# race against the API server starting to serve the new kind.
kubectl apply -f test/e2e/coffeeconfig-crd.yaml
kubectl wait --for=condition=Established crd/coffeeconfigs.examples.configbutler.ai --timeout=60s
kubectl apply -f ../voter/config/crd/
kubectl wait --for=condition=Established crd/quizsessions.examples.configbutler.ai crd/quizsubmissions.examples.configbutler.ai --timeout=60s
kubectl apply -f test/e2e/voter.yaml
kubectl -n room-pass rollout restart deployment/voter
kubectl -n room-pass rollout status deployment/voter --timeout=180s

docker build -f test/e2e/demo-client/Dockerfile -t room-pass-demo-client:dev .
k3d image import room-pass-demo-client:dev -c room-pass-e2e
kubectl apply -f test/e2e/demo-client.yaml
kubectl -n room-pass patch deployment demo-client --type=merge -p "{\"spec\":{\"template\":{\"spec\":{\"hostAliases\":[{\"ip\":\"$gateway\",\"hostnames\":[\"login.roompass.test\"]}]}}}}"
kubectl -n room-pass rollout restart deployment/demo-client
kubectl -n room-pass rollout status deployment/demo-client --timeout=120s

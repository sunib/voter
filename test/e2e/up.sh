#!/usr/bin/env bash
# Voter's end-to-end fixture: a disposable k3d cluster running the real
# application behind a real login -- Traefik, Dex, and a RELEASED Room Pass.
#
# Room Pass is not built here. It is its own project (sunib/room-pass) and this
# fixture consumes it the way the demo cluster does: the published image, the
# CRDs from the same tag, its deploy/base with this fixture's origins. All three
# are pinned in test/e2e/room-pass/kustomization.yaml; bump them there, together.
#
# Everything lives in the `voter` namespace, beside the app, as on
# k8s.koudijs.dev. Hosts are *.voter.test, published on 19443 -- not 18443,
# which Room Pass's own fixture holds, and the two often run side by side:
#   app.voter.test    the Voter application, and Room Pass's /join, /bind and
#                     /logout on the same host, as on demo.koudijs.dev
#   login.voter.test  the issuer: Room Pass in front of Dex
set -euo pipefail
cd "$(dirname "$0")/../.."
mkdir -p .local
chmod 700 .local
cluster=voter-e2e
network="k3d-${cluster}"
# Dedicated cluster and kubeconfig; never switch the user's current context.
if ! k3d cluster list -o json | python3 -c 'import json,sys;sys.exit(not any(c["name"]=="voter-e2e" for c in json.load(sys.stdin)))'; then
  openssl req -x509 -newkey rsa:2048 -nodes -keyout .local/tls.key -out .local/tls.crt -days 7 -subj /CN=voter-test -addext 'subjectAltName=DNS:app.voter.test,DNS:login.voter.test' >/dev/null 2>&1
  # A Docker volume works with a sibling Docker daemon (host paths need not match).
  docker volume create voter-e2e-config >/dev/null
  docker run --rm -i -v voter-e2e-config:/config alpine:3.21 sh -c 'cat > /config/ca.crt' < .local/tls.crt
  docker run --rm -i -v voter-e2e-config:/config alpine:3.21 sh -c 'cat > /config/audit-policy.yaml' < test/e2e/audit-policy.yaml
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
  docker run --rm -i -v voter-e2e-config:/config alpine:3.21 sh -c 'cat > /config/authentication-config.yaml' < .local/authentication-config.yaml
  docker network inspect "$network" >/dev/null 2>&1 || docker network create "$network" >/dev/null
  gateway=$(docker network inspect "$network" --format '{{(index .IPAM.Config 0).Gateway}}')
  # --timeout bounds the --wait. Without it a server that never becomes ready
  # blocks until the CI job's own timeout kills it, which reports as "tests
  # were cancelled" rather than "the cluster did not come up".
  k3d cluster create "$cluster" --image rancher/k3s:v1.31.5-k3s1 --servers 1 --agents 0 --wait --timeout 180s --network "$network" --host-alias "$gateway:login.voter.test" --host-alias "$gateway:app.voter.test" \
    --kubeconfig-update-default=false --kubeconfig-switch-context=false \
    --port '19443:443@server:0' \
    --volume 'voter-e2e-config:/etc/voter-e2e@server:0' \
    --k3s-arg '--kube-apiserver-arg=audit-policy-file=/etc/voter-e2e/audit-policy.yaml@server:0' \
    --k3s-arg '--kube-apiserver-arg=audit-log-path=/etc/voter-e2e/audit.log@server:0' \
    --k3s-arg '--kube-apiserver-arg=audit-log-maxsize=20@server:0' \
    --k3s-arg '--kube-apiserver-arg=authentication-config=/etc/voter-e2e/authentication-config.yaml@server:0'
fi
k3d kubeconfig get "$cluster" > .local/kubeconfig
chmod 600 .local/kubeconfig
# Resolve issuer inside the API-server container through the host's published TLS port.
gateway=$(docker network inspect "$network" --format '{{(index .IPAM.Config 0).Gateway}}')
# The devcontainer must be able to reach the cluster API's advertised address.
docker network connect "$network" "$(hostname)" 2>/dev/null || true
server_ip=$(docker inspect "${network}-server-0" --format "{{(index .NetworkSettings.Networks \"${network}\").IPAddress}}")
if [ -f /.dockerenv ]; then
  sed -i "s|server: .*|server: https://$server_ip:6443|" .local/kubeconfig
fi
export KUBECONFIG="$PWD/.local/kubeconfig"
kubectl wait --for=condition=Ready "node/${network}-server-0" --timeout=90s

# Room Pass, as released: CRDs, RBAC, Deployment and Service from the pinned
# tag, with this fixture's origins patched in. Kustomize fetches the tag over
# the network; nothing from a Room Pass checkout is read.
kubectl apply -k test/e2e/room-pass
kubectl wait --for=condition=Established \
  crd/rooms.room-pass.koudijs.dev \
  crd/participants.room-pass.koudijs.dev --timeout=60s
if ! kubectl -n voter get secret room-pass-cookie >/dev/null 2>&1; then
  openssl rand 32 > .local/hash-key
  openssl rand 32 > .local/block-key
  kubectl -n voter create secret generic room-pass-cookie --from-file=hash-key=.local/hash-key --from-file=block-key=.local/block-key
fi
kubectl -n voter create secret tls local-tls --cert=.local/tls.crt --key=.local/tls.key --dry-run=client -o yaml | kubectl apply -f -
ends_at=$(date -u -d '+4 hours' +%Y-%m-%dT%H:%M:%SZ)
cat <<YAML | kubectl apply -f -
apiVersion: room-pass.koudijs.dev/v1alpha1
kind: Room
metadata:
  name: demo
  namespace: voter
spec:
  title: Voter local fixture
  attributionNote: It labels the change you are about to make in the cluster.
  endsAt: "$ends_at"
  enrollment: Open
  maxParticipants: 300
  # The production audience group, so the fixture's RBAC reads like the demo's.
  audienceGroup: demo:voter-audience
  allowedReturnURLs: ['https://app.voter.test:19443/']
YAML
# Helm installs Traefik CRDs asynchronously during k3s bootstrap. Retry the
# wait itself, not just a lookup: a CRD caught in the instant after it is
# created has no status.conditions yet, and `kubectl wait` fails on that
# outright instead of waiting.
for attempt in $(seq 1 45); do
  if kubectl wait --for=condition=Established crd/middlewares.traefik.io --timeout=10s >/dev/null 2>&1; then break; fi
  sleep 2
done
kubectl wait --for=condition=Established crd/middlewares.traefik.io --timeout=30s
kubectl apply -f test/e2e/dex.yaml -f test/e2e/edge.yaml
kubectl -n voter rollout restart deployment/dex
kubectl -n voter rollout restart deployment/room-pass
kubectl -n voter rollout status deployment/room-pass --timeout=180s
kubectl -n voter rollout status deployment/dex --timeout=180s

# The application trusts the fixture CA to reach the issuer over the published port.
kubectl -n voter create configmap issuer-ca --from-file=ca.crt=.local/tls.crt --dry-run=client -o yaml | kubectl apply -f -

# Built from the repository root because the image contains both the Go backend
# and the Vue bundle.
docker build -t voter:dev -f voter/Dockerfile .
k3d image import voter:dev -c "$cluster"
# Every examples.configbutler.ai CRD from this repository, which is where all
# three are defined -- this fixture keeps no copy of its own, so an e2e run
# cannot pass against a schema the application no longer ships.
#
# Applied and AWAITED before voter.yaml: a CRD and a custom resource of that
# kind in one apply is a race the resource usually loses, rejected with "no
# matches for kind" because the API server is not serving the type yet.
kubectl apply -f voter/config/crd/
kubectl wait --for=condition=Established \
  crd/coffeeconfigs.examples.configbutler.ai \
  crd/quizsessions.examples.configbutler.ai \
  crd/quizsubmissions.examples.configbutler.ai --timeout=60s
kubectl apply -f test/e2e/voter.yaml
kubectl -n voter rollout restart deployment/voter
kubectl -n voter rollout status deployment/voter --timeout=180s

# krm-foyer, as released, beside Voter on the application's host
# (docs/krm-foyer-migration.md, step 1). Chart and image are pinned by digest;
# bump them together with krm-foyer-values.yaml's image.tag.
krm_foyer_chart=oci://ghcr.io/configbutler/charts/krm-foyer:0.3.0@sha256:40f68f1ba1477e09a9813d304991518f600310b1f31e3a600634ba3a0c1ad0d2
# The client secret of voter-fixture in dex.yaml: krm-foyer signs in as Voter's client.
kubectl -n voter create secret generic krm-foyer-oidc --from-literal=client-secret=voter-fixture-secret \
  --dry-run=client -o yaml | kubectl apply -f -
# Made once and kept, so sessions survive a rerun, as Voter's fixed cookie keys do.
if ! kubectl -n voter get secret krm-foyer-session-keys >/dev/null 2>&1; then
  kubectl -n voter create secret generic krm-foyer-session-keys \
    --from-literal=session-keys="$(head -c 32 /dev/urandom | base64)"
fi
helm upgrade --install krm-foyer "$krm_foyer_chart" --namespace voter \
  -f test/e2e/krm-foyer-values.yaml --wait --timeout 180s
printf 'Cluster ready. Kubeconfig: %s/.local/kubeconfig\n' "$PWD"

# Ready, from where the browser stands. A finished rollout says the pods are up;
# it does not say Traefik routes to them. In that gap Traefik answers for itself
# -- 404 before it has loaded a route, 502 while an endpoint still points at a
# pod that `rollout restart` is terminating -- and a browser test that starts
# there fails on its first page. Neither status says the application is up, so
# wait for what the browser gets: Voter's login, through Dex, ending on Room
# Pass's join page with its "Room code" field. Three times running, so one lucky
# answer from a pod on its way out does not count.
probe() {
  local jar body
  jar=$(mktemp); body=$(mktemp)
  status=$(curl -sk -L -c "$jar" -b "$jar" -o "$body" -w '%{http_code}' \
    --resolve "app.voter.test:19443:$gateway" \
    --resolve "login.voter.test:19443:$gateway" \
    https://app.voter.test:19443/auth/login || true)
  local ok=1
  if [ "$status" = 200 ] && grep -q 'Room code' "$body"; then ok=0; fi
  rm -f "$jar" "$body"
  [ "$ok" = 0 ] || return 1
  # And krm-foyer on the same host: a 401 for a request without a session, with
  # its own interruption header, so it is krm-foyer answering and not Voter.
  status=$(curl -sk -D - -o /dev/null --resolve "app.voter.test:19443:$gateway" \
    https://app.voter.test:19443/k8s/api || true)
  grep -q '^HTTP/[0-9.]* 401' <<<"$status" && grep -qi '^krm-foyer-interruption:' <<<"$status"
}
streak=0
for attempt in $(seq 1 90); do
  if probe; then
    streak=$((streak + 1))
    if [ "$streak" -ge 3 ]; then
      echo "Fixture serves the join page and krm-foyer through Traefik (attempt ${attempt})."
      exit 0
    fi
  else
    streak=0
  fi
  sleep 2
done
echo "ERROR: the join page and krm-foyer were not served three times running within 180s." >&2
exit 1

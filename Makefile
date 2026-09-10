# Legacy convenience targets, kept because they still work and are wired to the
# private zot.z65.nl registry used before ghcr.io.
#
# The maintained definition of how this repository is built, tested and shipped
# is Taskfile.yaml -- `task lint`, `task test`, `task build` -- and that is what
# CI runs. Prefer it. The two published images come from `task image-room-pass`
# and `task image-voter`; the equivalent of `build-push` below is:
#
#   task build REGISTRY=zot.z65.nl IMAGE_OWNER=voter TAG=coffee PUSH=true
#
.PHONY: build-voter test-voter frontend-dev-remote frontend-dev-remote-https build-push

login:
	docker login zot.z65.nl -u admin

build-voter:
	mkdir -p voter/bin
	cd voter && go build -o ./bin/voter ./...

test-voter:
	cd voter && go test ./...

frontend-dev-remote:
	cd frontend && VITE_DEV_API_ORIGIN=https://demo.configbutler.ai npm run dev

frontend-dev-remote-https:
	cd frontend && VITE_DEV_API_ORIGIN=https://demo.configbutler.ai VITE_DEV_HTTPS=1 npm run dev -- --host

# One image now: the frontend bundle is built into the voter image and served by
# the Go backend, so the context is the repo root and the Dockerfile is named
# explicitly. See voter/Dockerfile.
build-push:
	docker buildx build --push \
		--build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" \
		--build-arg GIT_DIRTY="$$(test -n "$$(git status --porcelain)" && echo 1 || echo 0)" \
		--build-arg BUILD_DATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--provenance=true \
		--sbom=true \
		-f voter/Dockerfile \
		-t zot.z65.nl/voter/voter:coffee .

.PHONY: build-auth-service test-auth-service debug-auth-service frontend-dev-remote frontend-dev-remote-https build-push-auth-service build-push-frontend build-push

login:
	docker login zot.z65.nl -u admin

build-auth-service:
	mkdir -p auth-service/bin
	cd auth-service && go build -o ./bin/auth-service ./...

test-auth-service:
	cd auth-service && go test ./...

frontend-dev-remote:
	cd frontend && VITE_DEV_API_ORIGIN=https://voter.z65.nl npm run dev

frontend-dev-remote-https:
	cd frontend && VITE_DEV_API_ORIGIN=https://voter.z65.nl VITE_DEV_HTTPS=1 npm run dev -- --host

build-push-auth-service:
	docker buildx build --push \
		--build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" \
		--build-arg GIT_DIRTY="$$(test -n "$$(git status --porcelain)" && echo 1 || echo 0)" \
		--build-arg BUILD_DATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--provenance=true \
		--sbom=true \
		-t zot.z65.nl/voter/auth-service:coffee ./auth-service

build-push-frontend:
	docker buildx build --push \
		--build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" \
		--build-arg GIT_DIRTY="$$(test -n "$$(git status --porcelain)" && echo 1 || echo 0)" \
		--build-arg BUILD_DATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--provenance=true \
		--sbom=true \
		-t zot.z65.nl/voter/frontend:coffee ./frontend

build-push: build-push-auth-service build-push-frontend

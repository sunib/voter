.PHONY: build-auth-service test-auth-service debug-auth-service build-push-auth-service build-push-frontend build-push

build-auth-service:
	mkdir -p auth-service/bin
	cd auth-service && go build -o ./bin/auth-service ./...

test-auth-service:
	cd auth-service && go test ./...

build-push-auth-service:
	docker buildx build --push \
		--build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" \
		--build-arg GIT_DIRTY="$$(test -n "$$(git status --porcelain)" && echo 1 || echo 0)" \
		--build-arg BUILD_DATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--provenance=true \
		--sbom=true \
		-t zot.z65.nl/present/auth-service:latest ./auth-service

build-push-frontend:
	docker buildx build --push \
		--build-arg GIT_COMMIT="$$(git rev-parse --short HEAD)" \
		--build-arg GIT_DIRTY="$$(test -n "$$(git status --porcelain)" && echo 1 || echo 0)" \
		--build-arg BUILD_DATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--provenance=true \
		--sbom=true \
		-t zot.z65.nl/present/frontend:latest ./frontend

build-push: build-push-auth-service build-push-frontend

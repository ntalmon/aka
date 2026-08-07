E2E_IMAGE := aka-e2e

.PHONY: e2e e2e-image test build fmt

build:
	go build ./...

test:
	go test -race -count=1 ./...

fmt:
	gofmt -w .

e2e-image:
	docker build -f Dockerfile.e2e -t $(E2E_IMAGE) .

# Runs the tagged suite inside the container. The module and build caches are
# named volumes so repeat runs do not recompile the world.
e2e: e2e-image
	docker run --rm \
	  -e AKA_E2E_INSIDE_CONTAINER=1 \
	  -v "$(CURDIR)":/src \
	  -v aka-e2e-gomod:/go/pkg/mod \
	  -v aka-e2e-gocache:/root/.cache/go-build \
	  -w /src \
	  $(E2E_IMAGE) \
	  go test -tags e2e -race -count=1 -v ./test/e2e/...

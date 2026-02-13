PROJECT     := github.com/chipmk/docker-mac-net-connect
SETUP_IMAGE := ghcr.io/chipmk/docker-mac-net-connect/setup
VERSION     := $(shell git describe --tags 2>/dev/null || echo dev)
LD_FLAGS    := -X $(PROJECT)/version.Version=$(VERSION) -X $(PROJECT)/version.SetupImage=$(SETUP_IMAGE)

.PHONY: run build run-go build-go build-docker build-push-docker vet lint

run: build-docker run-go
build: build-docker build-go

run-go:
	go run -ldflags "$(LD_FLAGS)" $(PROJECT)

build-go:
	go build -ldflags "-s -w $(LD_FLAGS)" $(PROJECT)

build-docker:
	docker build -t $(SETUP_IMAGE):$(VERSION) ./client

build-push-docker:
	docker buildx build --platform linux/amd64,linux/arm64 --push -t $(SETUP_IMAGE):$(VERSION) -t $(SETUP_IMAGE):latest ./client

vet:
	go vet ./...

lint:
	golangci-lint run ./...

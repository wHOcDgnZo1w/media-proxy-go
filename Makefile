.PHONY: build run stop logs clean rebuild shell test lint tidy

# Detect container runtime
CONTAINER_RUNTIME := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
ifeq ($(CONTAINER_RUNTIME),)
$(error "No container runtime found. Install podman or docker.")
endif

# Container settings
IMAGE_NAME := media-proxy-go
CONTAINER_NAME := media-proxy-go
PORT := 7860

# Build the container image
.PHONY: build
build:
	go mod tidy -v
	$(CONTAINER_RUNTIME) build -t $(IMAGE_NAME) -f Containerfile .

# Run the container
.PHONY: run
run: build
	$(CONTAINER_RUNTIME) run -d \
		--name $(CONTAINER_NAME) \
		-p $(PORT):7860 \
		-v media-proxy-recordings:/app/recordings \
		-e LOG_LEVEL=info \
		-e LOG_JSON=false \
		$(IMAGE_NAME)
	@echo "MediaProxy running at http://localhost:$(PORT)"

# Stop and remove the container
.PHONY: stop
stop:
	-$(CONTAINER_RUNTIME) stop $(CONTAINER_NAME)
	-$(CONTAINER_RUNTIME) rm $(CONTAINER_NAME)

# View container logs
.PHONY: logs
logs:
	$(CONTAINER_RUNTIME) logs -f $(CONTAINER_NAME)

# Remove container and image
.PHONY: clean
clean: stop
	-$(CONTAINER_RUNTIME) rmi $(IMAGE_NAME)
	-$(CONTAINER_RUNTIME) volume rm media-proxy-recordings

# Rebuild and run
rebuild: stop build run

# Open shell in running container
shell:
	$(CONTAINER_RUNTIME) exec -it $(CONTAINER_NAME) /bin/sh

# Run tests
test:
	go test -v ./...

# Run linter
lint:
	go vet ./...
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed"

# Tidy dependencies
tidy:
	go mod tidy

# Build binary locally
build-local:
	go build -o bin/media-proxy ./cmd/media-proxy

# Run locally
run-local: build-local
	./bin/media-proxy

# Show help
help:
	@echo "Container runtime: $(CONTAINER_RUNTIME)"
	@echo ""
	@echo "Available targets:"
	@echo "  build       - Build container image"
	@echo "  run         - Run container"
	@echo "  stop        - Stop and remove container"
	@echo "  logs        - Follow container logs"
	@echo "  clean       - Remove container, image, and volumes"
	@echo "  rebuild     - Stop, rebuild, and run"
	@echo "  shell       - Open shell in running container"
	@echo "  test        - Run Go tests"
	@echo "  lint        - Run linters"
	@echo "  tidy        - Tidy Go modules"
	@echo "  build-local - Build binary locally"
	@echo "  run-local   - Build and run locally"

APP_NAME := http
BUILD_DIR := runtime/bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X 'main.Version=$(VERSION)' -X 'main.BuildTime=$(BUILD_TIME)'

.PHONY: all build-cli build-api build-consumer build-service clean test

all: build-cli

build-cli:
	@echo "Building CLI..."
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-cli ./cmd/cli

build-api:
	@echo "Building API..."
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-api ./cmd/api

build-consumer:
	@echo "Building Consumer..."
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-consumer ./cmd/consumer

build-service:
	@echo "Building Service..."
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-service ./cmd/service

test:
	kue test --dir tests

clean:
	rm -rf $(BUILD_DIR)

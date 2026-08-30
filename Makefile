.PHONY: build run install install-user uninstall uninstall-user clean test fmt

BINARY_NAME := agent-deck
BUILD_DIR := build
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/agent-deck

run:
	go run ./cmd/agent-deck

install: build
	sudo install -m 0755 $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

install-user: build
	mkdir -p $(HOME)/.local/bin
	install -m 0755 $(BUILD_DIR)/$(BINARY_NAME) $(HOME)/.local/bin/$(BINARY_NAME)

uninstall:
	sudo rm -f /usr/local/bin/$(BINARY_NAME)

uninstall-user:
	rm -f $(HOME)/.local/bin/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)
	go clean

test:
	go test ./...

fmt:
	go fmt ./...

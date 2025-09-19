GO ?= go
BINARY_NAME ?= cmkms
CMD_PATH ?= ./cmd/cmkms
BUILD_DIR ?= bin
BUILD_PATH := $(BUILD_DIR)/$(BINARY_NAME)

.PHONY: build install clean test

build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_PATH) $(CMD_PATH)

install:
	$(GO) install $(CMD_PATH)

test:
	GOCACHE=$(mktemp -d) $(GO) test ./... -v

clean:
	rm -rf $(BUILD_DIR)

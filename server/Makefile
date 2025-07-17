SHELL := /bin/bash
.PHONY: oapi-generate build dev test clean

BIN_DIR        ?= $(CURDIR)/bin
OAPI_CODEGEN   ?= $(BIN_DIR)/oapi-codegen
RECORDING_DIR  ?= $(CURDIR)/recordings
OAPI_SPEC      ?= $(CURDIR)/openapi.yaml
OAPI_SPEC_30   ?= $(CURDIR)/openapi-3.0.yaml
OAPI_OUTPUT    ?= $(CURDIR)/server/lib/oapi.go

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(RECORDING_DIR):
	mkdir -p $(RECORDING_DIR)

$(OAPI_CODEGEN): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

$(OAPI_SPEC_30): $(OAPI_SPEC)
	pnpm i -g @apiture/openapi-down-convert
	openapi-down-convert --input $(OAPI_SPEC) --output $(OAPI_SPEC_30)

$(OAPI_OUTPUT): $(OAPI_SPEC_30) $(OAPI_CODEGEN)
	$(OAPI_CODEGEN) -config ./oapi-codegen.yaml $(OAPI_SPEC_30)
	go mod tidy

oapi-generate: $(OAPI_OUTPUT)

build: oapi-generate | $(BIN_DIR)
	go build -o $(BIN_DIR)/api ./cmd/api

dev: build $(RECORDING_DIR)
	OUTPUT_DIR=$(RECORDING_DIR) DISPLAY_NUM=$(DISPLAY_NUM) ./bin/api

test:
	go vet ./...
	go test -v -race ./...

clean:
	@rm -rf $(BIN_DIR)
	@rm -f openapi-3.0.yaml
	@echo "Clean complete"

DISPLAY_NUM := $(shell \
	if [ "$$(uname)" = "Linux" ]; then \
		echo "1"; \
	elif [ "$$(uname)" = "Darwin" ]; then \
		ffmpeg -f avfoundation -list_devices true -i "" 2>&1 | grep "Capture screen" | head -1 | sed 's/.*\[\([0-9]*\)\].*/\1/' 2>/dev/null || echo "2"; \
	else \
		echo "0"; \
	fi)

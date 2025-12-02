#!/bin/bash
set -e
export GOBIN=/workspace/kernel-images/server/bin
mkdir -p $GOBIN
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
$GOBIN/oapi-codegen -config ./oapi-codegen.yaml ./openapi-3.0.yaml
go run ./scripts/oapi/patch_sse_methods.go -file ./lib/oapi/oapi.go -expected-replacements 3
go fmt ./lib/oapi/oapi.go
go mod tidy
chown -R 1000:1000 .

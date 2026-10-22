VERSION ?= 0.0.0-dev
LDFLAGS := -s -w -X github.com/mattoddie/crunch-mcp/internal/tools.Version=$(VERSION)
export CGO_ENABLED := 0

# Platforms for the MCP Bundles (one .mcpb each), as GOOS/GOARCH.
MCPB_PLATFORMS := darwin/arm64 darwin/amd64 windows/amd64 windows/arm64 linux/amd64 linux/arm64

.PHONY: build test lint docs docs-check image mcpb server-json mcp-publisher clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/crunch-mcp ./cmd/crunch-mcp

test:
	go test ./...

lint:
	gofmt -l . | (! grep .) || (echo "Run gofmt -w ." && exit 1)
	go vet ./...

docs:
	go run ./cmd/gen-tool-docs
	go run ./cmd/mcpb manifest

docs-check:
	go run ./cmd/gen-tool-docs -check
	go run ./cmd/mcpb manifest -check
	go run ./cmd/mcpb server-json -check

image:
	docker build --build-arg VERSION=$(VERSION) -t crunch-mcp:$(VERSION) .

# MCP Bundles for Claude Desktop: dist/crunch-mcp-$(VERSION)-<os>-<arch>.mcpb.
mcpb:
	rm -rf .build/mcpb dist/*.mcpb
	go build -o .build/mcpb/pack ./cmd/mcpb
	set -e; for p in $(MCPB_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o .build/mcpb/$$os-$$arch/crunch-mcp$$ext ./cmd/crunch-mcp; \
		.build/mcpb/pack pack -version $(VERSION) -goos $$os -binary .build/mcpb/$$os-$$arch/crunch-mcp$$ext \
			-o dist/crunch-mcp-$(VERSION)-$$os-$$arch.mcpb; \
	done

# The MCP Registry entry for this version, with the hashes of the bundles in dist/.
server-json:
	go run ./cmd/mcpb server-json -version $(VERSION) -dist dist -o dist/server.json

# mcp-publisher, the MCP Registry CLI, pinned and checked against the release's checksums.
MCP_PUBLISHER_VERSION := 1.8.1
MCP_PUBLISHER_SHA256_linux_amd64 := a06c9096dcb9727c13555b6be26c7effa707b01f06a4c561ba7a3635443cf2cc
MCP_PUBLISHER_SHA256_linux_arm64 := 8dd75a6cf6845688b5d4e46df58d3ca26d5c8d233bb0626606e1db82c5e883e4
MCP_PUBLISHER_SHA256_darwin_amd64 := 88126981225e7714fcc6b7a10cdba4a80ae5901e9740a8c06d0d5195c8bc294c
MCP_PUBLISHER_SHA256_darwin_arm64 := e45e520892460732a4bdf37255576415d4a53ec171f8b913faf15bb1aef7cb77
MCP_PUBLISHER_PLATFORM = $(shell go env GOHOSTOS)_$(shell go env GOHOSTARCH)

mcp-publisher:
	mkdir -p .build/mcp-publisher
	curl -fsSL -o .build/mcp-publisher/mcp-publisher.tar.gz \
		https://github.com/modelcontextprotocol/registry/releases/download/v$(MCP_PUBLISHER_VERSION)/mcp-publisher_$(MCP_PUBLISHER_PLATFORM).tar.gz
	echo "$(MCP_PUBLISHER_SHA256_$(MCP_PUBLISHER_PLATFORM))  .build/mcp-publisher/mcp-publisher.tar.gz" | shasum -a 256 -c -
	tar -xzf .build/mcp-publisher/mcp-publisher.tar.gz -C .build/mcp-publisher mcp-publisher
	.build/mcp-publisher/mcp-publisher --help >/dev/null

clean:
	rm -rf bin .build dist

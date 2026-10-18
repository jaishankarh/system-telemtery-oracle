BIN := sys-telemetry-app
PKG := ./cmd/sys-telemetry-app
export CGO_ENABLED := 0

.PHONY: build build-nvml test clean
# build: the default binaries. Fully static: no interpreter, no shared libraries.
build:
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o dist/$(BIN)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o dist/$(BIN)-linux-arm64 $(PKG)
	@for f in dist/$(BIN)-linux-amd64 dist/$(BIN)-linux-arm64; do \
	  if readelf -lW $$f | grep -q INTERP; then echo "FAIL: $$f has an ELF interpreter (not static)"; exit 1; fi; \
	  if readelf -dW $$f 2>/dev/null | grep -q NEEDED; then echo "FAIL: $$f has NEEDED shared libraries (not static)"; exit 1; fi; \
	  echo "static OK: $$f"; \
	done

# build-nvml: adds NVIDIA support (NVML loaded at runtime). NOT static: it links the
# vendored dlopen loader, so it needs glibc (see docs/adr/0002-addendum-vendored-purego.md).
build-nvml:
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -tags nvml -trimpath -ldflags '-s -w' -o dist/$(BIN)-nvml-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -tags nvml -trimpath -ldflags '-s -w' -o dist/$(BIN)-nvml-linux-arm64 $(PKG)

test:
	go vet ./...
	go test ./...
	go vet -tags nvml ./...
	go test -tags nvml ./internal/gpu/... ./internal/server/... ./internal/cli/...

clean:
	rm -rf dist

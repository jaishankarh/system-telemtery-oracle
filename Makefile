BIN := sys-telemetry-app
PKG := ./cmd/sys-telemetry-app
export CGO_ENABLED := 0

.PHONY: build test clean
build:
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o dist/$(BIN)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o dist/$(BIN)-linux-arm64 $(PKG)

test:
	go vet ./...
	go test ./...

clean:
	rm -rf dist

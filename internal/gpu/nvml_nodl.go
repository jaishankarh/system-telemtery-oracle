//go:build cgo

package gpu

import "errors"

// The release binary is built with CGO_ENABLED=0 (ADR 0002). A cgo build (for
// example a developer's plain `go build`) has no runtime loader: no NVIDIA data.
func openDriverLibrary() (nvmlAPI, error) { return nil, errors.New("NVML loading needs CGO_ENABLED=0") }

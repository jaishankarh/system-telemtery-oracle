//go:build nvml && cgo

package gpu

import "errors"

// The nvml build loads the library with the vendored loader, which needs
// CGO_ENABLED=0. A cgo build gets no NVIDIA data.
func openDriverLibrary() (nvmlAPI, error) {
	return nil, errors.New("the nvml build must be built with CGO_ENABLED=0")
}

# ADR 0002 addendum: NVML support is a separate build (`nvml` tag), the default stays static

The module proxy is unreachable in the build environment, so `github.com/ebitengine/purego` (Apache-2.0, commit 715acd0, Oct 2026) is vendored, trimmed to linux/amd64 and linux/arm64 without cgo, under `internal/third_party/purego` (licence kept). Two local edits: the import path, and `reflect.TypeAssert` (Go 1.25) replaced by a type assertion so it builds on Go 1.24.

## Why two builds

Loading a library at runtime without cgo needs purego's "fakecgo" (the runtime's cgo hooks written in Go and assembly, so C code gets thread-local storage) and dynamic imports of libdl/libc. Linking that makes the binary a dynamically linked ELF that needs glibc. That breaks the static, no-runtime-dependency promise (SPEC, ADR 0002, ticket #2). So the loader is behind the build tag `nvml`:

- **Default build** (`make build`, no tag, `CGO_ENABLED=0`): fully static, nothing from purego is linked. `gpu.NewNVML()` returns a reader with no NVIDIA data whose `Unavailable()` reason says NVIDIA support needs the nvml build. When NVIDIA hardware or driver is present (`/proc/driver/nvidia/version` or a DRM card with vendor 0x10de) that reason is listed under "Unavailable on this machine" (item `nvidia_gpu`, ADR 0006). AMD and Intel sysfs reading and the Machine GPU seam are in the default build. `make build` fails if the output has an ELF interpreter or NEEDED entries.
- **nvml build** (`make build-nvml`, `-tags nvml`): `sys-telemetry-app-nvml-linux-{amd64,arm64}`. Adds the NVML reader. Released as extra assets; `install.sh` installs it only with `SYS_TELEMETRY_VARIANT=nvml`.

## How the nvml build works and how it stays safe

`dlopen("libnvidia-ml.so.1")` and `dlsym` give NVML function pointers, called through purego's `SyscallN`. It opens the library only when the driver is present, retries a failed load every ~5 minutes, recovers from panics, and gives up on a call that blocks longer than 1.5 s. Any failure means no NVIDIA data and an unavailable reason, never a crash.

## Risks

- The nvml build is dynamically linked: it needs glibc (`libc.so.6`, `libdl.so.2`, `libpthread.so.0`). It does not start on musl systems such as Alpine; use the static build there (no NVIDIA data).
- fakecgo depends on Go runtime internals (linkname). A Go upgrade can break the nvml build; re-vendor purego from upstream then. The default build is unaffected, and CI builds both so a break shows up at release time.
- The NVIDIA path has been tested only against a stub library, not real NVML, until checked on the NVIDIA machine.
- Reading NVML every sample may wake a runtime-suspended hybrid GPU.

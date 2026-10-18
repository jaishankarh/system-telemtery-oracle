# Go, with GPU libraries loaded at runtime

Written in Go and built with CGO disabled so one static binary runs on any Linux. NVIDIA's NVML is loaded at runtime (purego-style dlopen) when present instead of being linked, so the binary still starts on machines without a GPU or driver; AMD and Intel GPUs are read from sysfs and need no library. Rust was the alternative; Go was chosen for build speed and a simpler cross-compile story.

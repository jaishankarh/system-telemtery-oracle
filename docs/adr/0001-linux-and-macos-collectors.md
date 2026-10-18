# Linux first; macOS deferred to phase 2 behind a collector seam

Phase 1 targets any Linux (x86_64 and arm64) only. macOS is wanted but deferred: it has no per-process PSS, swap or pressure stall data, and Docker containers there are hidden inside one VM process, so it needs its own collector and a reduced metric set. The collector sits behind a platform interface from day one so the macOS collector can be added without reshaping the rest. Windows is out of scope.

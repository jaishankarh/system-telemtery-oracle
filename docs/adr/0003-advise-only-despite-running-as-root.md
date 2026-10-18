# Advise only, even though it runs as root

The tool runs as root (to read every process's memory maps) but never changes the system: no sysctl writes, no container limits, no killing or renicing. Fixes are shown as exact commands or config to copy. This keeps a root process exposed over HTTP from being an attack surface for system changes. The dashboard binds to 127.0.0.1 only, with no auth; remote viewing goes through an SSH tunnel.

The one exception is the tool's own process: it exempts itself from the kernel OOM killer so it can keep recording during memory exhaustion, and runs under a hard memory cap of its own so a bug in it can never become the problem.

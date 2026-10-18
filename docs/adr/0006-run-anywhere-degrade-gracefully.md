# Run on any Linux and degrade instead of refusing

The binary starts on old kernels and distros without cgroup v2 or pressure stall data rather than requiring a modern baseline. Whatever the kernel can't provide is hidden, and Advice that depends on it is disabled with a stated reason, so every code path must treat each data source as optional. Requiring cgroup v2 + PSI would have been much simpler but breaks the "any device" goal.

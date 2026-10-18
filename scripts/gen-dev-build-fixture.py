#!/usr/bin/env python3
"""Regenerate testdata/fixtures/dev-box-dev-build: the dev-box-applications
fixture plus a recorded editor with a built-in terminal running a dev server,
two projects' builds, a build outside any VCS project and a Gradle daemon.
Run scripts/gen-applications-fixture.py first. Usage: scripts/gen-dev-build-fixture.py"""
import os, shutil
here = os.path.dirname(os.path.abspath(__file__))
base = os.path.join(here, "..", "testdata", "fixtures")
src, dst = os.path.join(base, "dev-box-applications"), os.path.join(base, "dev-box-dev-build")
shutil.rmtree(dst, ignore_errors=True)
shutil.copytree(src, dst, symlinks=True)
U = "/user.slice/user-1000.slice"
UM = U + "/user@1000.service"
CURSOR = UM + "/app.slice/app-gnome-cursor-5150.scope"
VTE2 = UM + "/app.slice/vte-spawn-9f9f.scope"
# project directories: shop and tool are VCS roots; scratch is not.
# NOTE: git cannot track anything under a '.git' path component, so these are
# recreated at test time by newDevBuildMachine in internal/cli/devbuild_test.go.
for d in ["home/dev/shop/.git", "home/dev/shop/web", "home/dev/shop/api", "home/dev/tool/.git",
          "home/dev/tool/crates/core", "home/dev/scratch"]:
    os.makedirs(os.path.join(dst, d), exist_ok=True)
for d in ["home/dev/shop/.git", "home/dev/tool/.git"]:  # git does not store empty directories
    open(os.path.join(dst, d, "HEAD"), "w").write("ref: refs/heads/main\n")
# pid, ppid, comm, argv, cgroup, cwd, rss, pss, swap (kB)
P = [
 (1000, 50, "cursor", ["/usr/share/cursor/cursor"], CURSOR, "/home/dev/shop", 900000, 600000, 50000),
 (1001, 1000, "cursor", ["/usr/share/cursor/cursor", "--type=ptyHost"], CURSOR, "/home/dev/shop", 100000, 60000, 0),
 (1002, 1001, "bash", ["/bin/bash"], CURSOR, "/home/dev/shop", 6000, 3000, 0),
 (1003, 1002, "npm run dev", ["npm", "run", "dev"], CURSOR, "/home/dev/shop/web", 90000, 70000, 0),
 (1004, 1003, "sh", ["sh", "-c", "vite"], CURSOR, "/home/dev/shop/web", 3000, 1000, 0),
 (1005, 1004, "node", ["node", "/home/dev/shop/web/node_modules/.bin/vite"], CURSOR, "/home/dev/shop/web", 400000, 300000, 0),
 (1006, 1000, "node", ["node", "/usr/share/cursor/resources/tsserver.js"], CURSOR, "/home/dev/shop", 300000, 200000, 0),
 (1007, 1002, "node", ["node", "/home/dev/shop/api/node_modules/.bin/nodemon", "server.js"], CURSOR, "/home/dev/shop/api", 120000, 90000, 0),
 (1010, 410, "bash", ["bash"], VTE2, "/home/dev/tool", 6000, 3000, 0),
 (1011, 1010, "cargo", ["cargo", "build", "--release"], VTE2, "/home/dev/tool/crates/core", 80000, 50000, 0),
 (1012, 1011, "rustc", ["rustc", "--crate-name", "core"], VTE2, "/home/dev/tool/crates/core", 700000, 650000, 0),
 (1013, 1010, "make", ["make", "-j4"], VTE2, "/home/dev/scratch", 4000, 2000, 0),
 (1020, 50, "java", ["/usr/lib/jvm/java/bin/java", "org.gradle.launcher.daemon.bootstrap.GradleDaemon", "8.5"], UM + "/app.slice/gradle-daemon.scope", "/home/dev/shop", 500000, 400000, 0),
]
def w(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w").write(text)
for i, (pid, ppid, comm, argv, cg, cwd, rss, pss, swap) in enumerate(P):
    d = os.path.join(dst, "proc", str(pid))
    w(d + "/stat", "%d (%s) S %d %d %d 0 -1 4194304 100 0 0 0 100 50 0 0 20 0 4 0 %d 4595712 3000 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n" % (pid, comm, ppid, pid, pid, 5000 + i))
    w(d + "/status", "Name:\t%s\nTgid:\t%d\nPid:\t%d\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t%d kB\nVmSwap:\t%d kB\nThreads:\t4\n" % (comm, pid, pid, rss, swap))
    w(d + "/cmdline", "\0".join(argv) + "\0")
    w(d + "/cgroup", "0::%s\n" % cg)
    os.symlink("/" + "/".join([p for p in ("", *cwd.split("/")) if p]), d + "/cwd")
    w(d + "/smaps_rollup", "00400000-7ffc18f63000 ---p 00000000 00:00 0   [rollup]\nRss:  %d kB\nPss:  %d kB\nSwap:  %d kB\n" % (rss, pss, swap))
    w(d + "/io", "read_bytes: 1000\nwrite_bytes: 2000\n")

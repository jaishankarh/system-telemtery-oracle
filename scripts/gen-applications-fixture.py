#!/usr/bin/env python3
"""Regenerate testdata/fixtures/dev-box-applications: the dev-box-process-tree
fixture's system files plus a larger recorded process tree with cgroup paths
and working directories. Usage: scripts/gen-applications-fixture.py"""
import os, shutil
here = os.path.dirname(os.path.abspath(__file__))
base = os.path.join(here, "..", "testdata", "fixtures")
src, dst = os.path.join(base, "dev-box-process-tree"), os.path.join(base, "dev-box-applications")
shutil.rmtree(dst, ignore_errors=True)
shutil.copytree(src, dst, ignore=shutil.ignore_patterns("fd"))
for n in os.listdir(os.path.join(dst, "proc")):
    if n.isdigit():
        shutil.rmtree(os.path.join(dst, "proc", n))
U = "/user.slice/user-1000.slice"
UM = U + "/user@1000.service"
SESS = U + "/session-3.scope"
VTE = UM + "/app.slice/vte-spawn-1b2c.scope"
# pid, ppid, comm, argv, cgroup, cwd, uid, rss, pss, swap (kB)
P = [
 (1, 0, "systemd", ["/sbin/init", "splash"], "/init.scope", "/", 0, 12000, 9000, 0),
 (2, 0, "kthreadd", None, "/", "/", 0, None, None, None),
 (3, 2, "kworker/0:0", None, "/", "/", 0, None, None, None),
 (50, 1, "systemd", ["/lib/systemd/systemd", "--user"], UM + "/init.scope", "/", 1000, 9000, 6000, 0),
 (100, 50, "code", ["/usr/share/code/code", "--type=extensionHost"], UM + "/app.slice/app-gnome-code-4242.scope", "/home/dev/my proj", 1000, 800000, 500000, 120000),
 (101, 100, "node", ["node", "/usr/share/code/tsserver.js"], UM + "/app.slice/app-gnome-code-4242.scope", "/home/dev/my proj", 1000, 300000, 200000, 0),
 (200, 50, "firefox", ["/usr/lib/firefox/firefox"], UM + "/app.slice/app-flatpak-org.mozilla.firefox-777.scope", "/home/dev", 1000, 400000, 300000, 100000),
 (201, 200, "Web Content", ["/usr/lib/firefox/firefox", "-contentproc", "-childID", "1"], UM + "/app.slice/app-flatpak-org.mozilla.firefox-777.scope", "/home/dev", 1000, 600000, 350000, 900000),
 (410, 50, "gnome-terminal-", ["/usr/libexec/gnome-terminal-server"], UM + "/app.slice/app-gnome-org.gnome.Terminal-900.scope", "/home/dev", 1000, 50000, 30000, 0),
 (400, 410, "bash", ["bash"], VTE, "/home/dev/my proj", 1000, 6000, 3000, 0),
 (300, 400, "node (dev)", ["node", "server.js"], VTE, "/home/dev/my proj/api", 1000, 200000, 150000, 0),
 (301, 300, "esbuild", ["esbuild", "--service"], VTE, "/home/dev/my proj/api", 1000, 40000, 30000, 0),
 (500, 50, "spotify", ["/snap/spotify/current/usr/share/spotify/spotify"], UM + "/app.slice/snap.spotify.spotify-5f2a.scope", "/home/dev", 1000, 250000, 180000, 0),
 (501, 500, "spotify", ["/snap/spotify/current/usr/share/spotify/spotify", "--type=zygote"], UM + "/app.slice/snap.spotify.spotify-5f2a.scope", "/home/dev", 1000, 90000, 60000, 0),
 (600, 1, "sshd", ["sshd: /usr/sbin/sshd -D"], "/system.slice/ssh.service", "/", 0, 8000, 5000, 0),
 (700, 1, "mystery-tool", ["/opt/mystery/mystery-tool", "--daemon"], SESS, "/opt/mystery", 1000, 70000, 50000, 0),
 (701, 700, "mystery-helper", ["/opt/mystery/helper"], SESS, "/opt/mystery", 1000, 20000, 10000, 0),
 (800, 1, "", [], SESS, "/", 1000, 5000, 4000, 0),
]
def w(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w").write(text)
for i, (pid, ppid, comm, argv, cg, cwd, uid, rss, pss, swap) in enumerate(P):
    d = os.path.join(dst, "proc", str(pid))
    w(d + "/stat", "%d (%s) S %d %d %d 0 -1 4194304 100 0 0 0 100 50 0 0 20 0 4 0 %d 4595712 3000 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n" % (pid, comm, ppid, pid, pid, 1000 + i))
    st = "Name:\t%s\nTgid:\t%d\nPid:\t%d\nUid:\t%d\t%d\t%d\t%d\n" % (comm, pid, pid, uid, uid, uid, uid)
    if rss is not None:
        st += "VmRSS:\t%d kB\nVmSwap:\t%d kB\n" % (rss, swap)
    w(d + "/status", st + "Threads:\t4\n")
    w(d + "/cmdline", "\0".join(argv) + "\0" if argv else "")
    w(d + "/cgroup", "0::%s\n" % cg)
    os.symlink(cwd, d + "/cwd")
    if rss is not None:
        w(d + "/smaps_rollup", "00400000-7ffc18f63000 ---p 00000000 00:00 0   [rollup]\nRss:  %d kB\nPss:  %d kB\nSwap:  %d kB\n" % (rss, pss, swap))
        w(d + "/io", "read_bytes: 1000\nwrite_bytes: 2000\n")

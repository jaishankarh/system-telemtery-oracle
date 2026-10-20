#!/usr/bin/env python3
"""Regenerate testdata/fixtures/dev-box-profiles: the dev-box-process-tree
fixture's system files plus a recorded desktop running every built-in App
Profile (editors, JetBrains IDEs, browsers, Gradle daemon, Android emulator).
Usage: scripts/gen-profiles-fixture.py"""
import os, shutil
here = os.path.dirname(os.path.abspath(__file__))
base = os.path.join(here, "..", "testdata", "fixtures")
src, dst = os.path.join(base, "dev-box-process-tree"), os.path.join(base, "dev-box-profiles")
shutil.rmtree(dst, ignore_errors=True)
shutil.copytree(src, dst, ignore=shutil.ignore_patterns("fd"))
for n in os.listdir(os.path.join(dst, "proc")):
    if n.isdigit():
        shutil.rmtree(os.path.join(dst, "proc", n))
UM = "/user.slice/user-1000.slice/user@1000.service"
def scope(name): return UM + "/app.slice/" + name + ".scope"
VTE = scope("vte-spawn-1b2c")
CUR, COD, WIN = scope("app-gnome-cursor-11"), scope("app-gnome-code-12"), scope("app-gnome-windsurf-13")
AS, IJ = scope("app-gnome-android\\x2dstudio-14"), scope("app-gnome-intellij\\x2didea-15")
CHR, CHM = scope("app-gnome-google\\x2dchrome-16"), scope("app-gnome-chromium-17")
FFX = scope("app-flatpak-org.mozilla.firefox-18")
H, P = "/home/dev", "/home/dev/proj"
CD = "--type=%s"
# pid, ppid, comm, argv, cgroup, cwd, rss, pss, swap (kB)
T = [
 (1, 0, "systemd", ["/sbin/init"], "/init.scope", "/", 12000, 9000, 0),
 (2, 0, "kthreadd", None, "/", "/", None, None, None),
 (50, 1, "systemd", ["/lib/systemd/systemd", "--user"], UM + "/init.scope", "/", 9000, 6000, 0),
 # Cursor: main, zygote, gpu, renderer, extension host with language servers and ripgrep, file watcher, terminal
 (100, 50, "cursor", ["/usr/share/cursor/cursor"], CUR, P, 300000, 200000, 0),
 (101, 100, "cursor", ["/usr/share/cursor/cursor", CD % "zygote"], CUR, P, 60000, 20000, 0),
 (102, 100, "cursor", ["/usr/share/cursor/cursor", CD % "gpu-process"], CUR, P, 200000, 100000, 0),
 (103, 101, "cursor", ["/usr/share/cursor/cursor", CD % "renderer"], CUR, P, 500000, 400000, 50000),
 (104, 100, "cursor", ["/usr/share/cursor/cursor", CD % "extensionHost"], CUR, P, 700000, 500000, 80000),
 (105, 104, "rust-analyzer", [H + "/.cursor/extensions/rust-lang.rust-analyzer-0.3.2/server/rust-analyzer"], CUR, P, 900000, 800000, 100000),
 (106, 104, "node", ["node", "/usr/share/cursor/resources/app/extensions/node_modules/typescript/lib/tsserver.js"], CUR, P, 400000, 300000, 0),
 (107, 104, "rg", ["/usr/share/cursor/resources/app/node_modules/@vscode/ripgrep/bin/rg", "--files"], CUR, P, 40000, 30000, 0),
 (108, 100, "cursor", ["/usr/share/cursor/cursor", CD % "fileWatcher"], CUR, P, 120000, 90000, 0),
 (109, 100, "cursor", ["/usr/share/cursor/cursor", CD % "ptyHost"], CUR, P, 50000, 30000, 0),
 (110, 109, "bash", ["/bin/bash"], CUR, P, 6000, 3000, 0),
 (111, 110, "node", ["node", "server.js"], CUR, P + "/api", 200000, 150000, 0),
 (112, 111, "esbuild", ["esbuild", "--service"], CUR, P + "/api", 40000, 30000, 0),
 # VS Code
 (200, 50, "code", ["/usr/share/code/code"], COD, P, 280000, 180000, 0),
 (201, 200, "code", ["/usr/share/code/code", CD % "extensionHost"], COD, P, 600000, 400000, 0),
 (202, 201, "gopls", ["/home/dev/go/bin/gopls", "-mode=stdio"], COD, P, 500000, 350000, 20000),
 (203, 201, "rg", ["/usr/share/code/resources/app/node_modules/@vscode/ripgrep/bin/rg", "-l", "TODO"], COD, P, 30000, 20000, 0),
 # Windsurf
 (250, 50, "windsurf", ["/opt/Windsurf/windsurf"], WIN, P, 250000, 170000, 0),
 (251, 250, "windsurf", ["/opt/Windsurf/windsurf", CD % "extensionHost"], WIN, P, 500000, 300000, 0),
 (252, 251, "language_server", ["/opt/Windsurf/resources/app/extensions/windsurf/bin/language_server_linux_x64", "--enable_lsp"], WIN, P, 1200000, 900000, 0),
 # Android Studio with its Gradle daemon, Kotlin daemon, file watcher; and the emulator it launched
 (300, 50, "java", ["/opt/android-studio/jbr/bin/java", "-Xmx2048m", "-Didea.paths.selector=AndroidStudio2024.1", "com.intellij.idea.Main"], AS, P, 2500000, 2000000, 300000),
 (301, 300, "fsnotifier", ["/opt/android-studio/bin/fsnotifier"], AS, P, 4000, 2000, 0),
 (302, 300, "java", ["/usr/lib/jvm/java-17/bin/java", "-XX:MaxMetaspaceSize=512m", "org.gradle.launcher.daemon.bootstrap.GradleDaemon", "8.5"], AS, P, 1500000, 1200000, 200000),
 (303, 302, "java", ["/usr/lib/jvm/java-17/bin/java", "org.jetbrains.kotlin.daemon.KotlinCompileDaemon"], AS, P, 800000, 600000, 0),
 (310, 300, "emulator", [H + "/Android/Sdk/emulator/emulator", "-avd", "Pixel_7"], AS, P, 100000, 60000, 0),
 (311, 310, "qemu-system-x86", [H + "/Android/Sdk/emulator/qemu/linux-x86_64/qemu-system-x86_64", "-avd", "Pixel_7"], AS, P, 3000000, 2500000, 0),
 # IntelliJ IDEA with a Maven server and a shell in its terminal running a build
 (320, 50, "java", ["/opt/idea/jbr/bin/java", "-Didea.paths.selector=IntelliJIdea2024.2", "com.intellij.idea.Main"], IJ, P, 2000000, 1500000, 0),
 (321, 320, "java", ["/opt/idea/jbr/bin/java", "org.jetbrains.idea.maven.server.RemoteMavenServer36"], IJ, P, 600000, 400000, 0),
 (322, 320, "zsh", ["/bin/zsh"], IJ, P, 7000, 4000, 0),
 (323, 322, "java", ["/usr/lib/jvm/java-17/bin/java", "-jar", "build/app.jar"], IJ, P, 500000, 350000, 0),
 # standalone Gradle daemon (its parent is gone) with a Kotlin daemon child
 (400, 1, "java", ["/usr/lib/jvm/java-17/bin/java", "org.gradle.launcher.daemon.bootstrap.GradleDaemon", "8.5"], scope("app-gnome-gnome\\x2dterminal-19"), P + "/svc", 1100000, 900000, 100000),
 (401, 400, "java", ["/usr/lib/jvm/java-17/bin/java", "org.jetbrains.kotlin.daemon.KotlinCompileDaemon"], scope("app-gnome-gnome\\x2dterminal-19"), P + "/svc", 500000, 400000, 0),
 # Chrome
 (500, 50, "chrome", ["/opt/google/chrome/chrome"], CHR, H, 300000, 200000, 0),
 (501, 500, "chrome", ["/opt/google/chrome/chrome", CD % "zygote"], CHR, H, 40000, 10000, 0),
 (502, 501, "chrome", ["/opt/google/chrome/chrome", CD % "gpu-process"], CHR, H, 250000, 120000, 0),
 (503, 501, "chrome", ["/opt/google/chrome/chrome", CD % "renderer"], CHR, H, 400000, 300000, 0),
 (504, 501, "chrome", ["/opt/google/chrome/chrome", CD % "renderer"], CHR, H, 450000, 350000, 90000),
 (505, 500, "chrome", ["/opt/google/chrome/chrome", CD % "utility", "--utility-sub-type=network.mojom.NetworkService"], CHR, H, 90000, 60000, 0),
 (506, 1, "chrome_crashpad", ["/opt/google/chrome/chrome_crashpad_handler", "--monitor-self"], CHR, H, 8000, 3000, 0),
 (507, 501, "chrome", ["/opt/google/chrome/chrome", CD % "renderer", "--extension-process"], CHR, H, 150000, 100000, 0),
 # Chromium (snap path), Brave, Edge
 (550, 50, "chrome", ["/snap/chromium/2890/usr/lib/chromium-browser/chrome"], CHM, H, 250000, 150000, 0),
 (551, 550, "chrome", ["/snap/chromium/2890/usr/lib/chromium-browser/chrome", CD % "renderer"], CHM, H, 300000, 200000, 0),
 (560, 50, "brave", ["/opt/brave.com/brave/brave"], scope("app-gnome-brave-20"), H, 280000, 180000, 0),
 (561, 560, "brave", ["/opt/brave.com/brave/brave", CD % "renderer"], scope("app-gnome-brave-20"), H, 350000, 250000, 0),
 (570, 50, "msedge", ["/opt/microsoft/msedge/msedge"], scope("app-gnome-microsoft\\x2dedge-21"), H, 260000, 160000, 0),
 (571, 570, "msedge", ["/opt/microsoft/msedge/msedge", CD % "renderer"], scope("app-gnome-microsoft\\x2dedge-21"), H, 330000, 230000, 0),
 # Firefox (flatpak scope): content processes by -contentproc
 (600, 50, "firefox", ["/usr/lib/firefox/firefox"], FFX, H, 400000, 300000, 100000),
 (601, 600, "Web Content", ["/usr/lib/firefox/firefox", "-contentproc", "-childID", "1", "-isForBrowser", "tab"], FFX, H, 600000, 350000, 900000),
 (602, 600, "Isolated Web Co", ["/usr/lib/firefox/firefox", "-contentproc", "-childID", "2", "tab"], FFX, H, 300000, 200000, 0),
 (603, 600, "RDD Process", ["/usr/lib/firefox/firefox", "-contentproc", "-childID", "3", "rdd"], FFX, H, 80000, 50000, 0),
 (604, 600, "GPU Process", ["/usr/lib/firefox/firefox", "-contentproc", "-childID", "4", "gpu"], FFX, H, 120000, 80000, 0),
 # not profiled: a terminal with its own dev server, and an unrelated process named like an editor helper
 (700, 50, "gnome-terminal-", ["/usr/libexec/gnome-terminal-server"], scope("app-gnome-org.gnome.Terminal-30"), H, 50000, 30000, 0),
 (701, 700, "bash", ["bash"], VTE, P, 6000, 3000, 0),
 (702, 701, "node", ["node", "vite"], VTE, P + "/web", 300000, 250000, 0),
 (710, 1, "mystery-tool", ["/opt/mystery/mystery-tool", "--daemon"], scope("session-3"), "/opt/mystery", 70000, 50000, 0),
]
def w(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    open(path, "w").write(text)
for i, (pid, ppid, comm, argv, cg, cwd, rss, pss, swap) in enumerate(T):
    d = os.path.join(dst, "proc", str(pid))
    w(d + "/stat", "%d (%s) S %d %d %d 0 -1 4194304 100 0 0 0 100 50 0 0 20 0 4 0 %d 4595712 3000 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n" % (pid, comm, ppid, pid, pid, 1000 + i))
    st = "Name:\t%s\nTgid:\t%d\nPid:\t%d\nUid:\t1000\t1000\t1000\t1000\n" % (comm, pid, pid)
    if rss is not None:
        st += "VmRSS:\t%d kB\nVmSwap:\t%d kB\n" % (rss, swap)
    w(d + "/status", st + "Threads:\t4\n")
    w(d + "/cmdline", "\0".join(argv) + "\0" if argv else "")
    w(d + "/cgroup", "0::%s\n" % cg)
    os.symlink(cwd, d + "/cwd")
    if rss is not None:
        w(d + "/smaps_rollup", "00400000-7ffc18f63000 ---p 00000000 00:00 0   [rollup]\nRss:  %d kB\nPss:  %d kB\nSwap:  %d kB\n" % (rss, pss, swap))
        w(d + "/io", "read_bytes: 1000\nwrite_bytes: 2000\n")

# sys-telemetry-app

A lightweight Linux monitor that rolls processes up into the applications people actually think about, and explains whether memory trouble is fixable by settings or needs more RAM.

## Grouping

**Process**:
A single running OS process, identified by its PID.
_Avoid_: task, job

**Application**:
A named group of processes shown as one row, such as a container, an editor, a browser or a known system service. Every process belongs to exactly one Application.
_Avoid_: app bundle, macro-application, process group

**Container**:
An Application made of every process inside one Docker or containerd container, named by the container name.
_Avoid_: stack, service

**Pod**:
An Application made of every container in one Kubernetes pod (k3s, kind), named by the pod name.
_Avoid_: deployment, workload

**Project**:
A source folder on disk that dev servers and builds run from, identified by the process's working directory.
_Avoid_: repo, workspace

**Dev & Build**:
An Application per Project holding its dev servers, compilers and build jobs, wherever they were launched from (including an editor's built-in terminal).
_Avoid_: terminal tooling, build systems

**App Profile**:
A built-in recogniser for a well-known program that folds its helper processes into one Application, such as an editor with its language servers.
_Avoid_: known app, signature, preset

**Other**:
The Application holding processes that no grouping rule could name, such as stray orphans.
_Avoid_: misc, unknown

## Advice

**Advice**:
A single finding about the running system, always either a Settings Fix or a Hardware Limit.
_Avoid_: alert, recommendation, diagnosis

**Settings Fix**:
Advice that a memory or CPU problem can be solved by changing configuration, with the exact change to make.
_Avoid_: tweak, optimization

**Hardware Limit**:
Advice that the machine needs more physical RAM, because the Working Set does not fit even with sensible settings.
_Avoid_: hardware bottleneck, upgrade warning

**Working Set**:
The memory every Application needs at once: the sum of each Application's PSS plus its swapped-out memory.
_Avoid_: active memory, footprint, demand

**Severity**:
How serious a piece of Advice is right now: info, warning or critical.
_Avoid_: priority, level

**Hidden**:
Advice the user has marked as known; it stays out of view until its Severity rises above the level at which it was hidden.
_Avoid_: dismissed, muted, ignored

**Snoozed**:
Advice hidden for a set period, after which it returns if still active.
_Avoid_: deferred, paused

**Memory Deficit**:
How much more memory the Working Set needs than the machine physically has.
_Avoid_: shortfall, overage

## Memory

**Disk Swap**:
Swap space on a disk or SSD partition or file; slow and wears storage.
_Avoid_: swap file, pagefile

**Compressed Swap**:
Swap kept compressed in RAM (zram or zswap); fast, no disk wear, costs CPU.
_Avoid_: zram swap, RAM swap

**Thrashing**:
Pages being swapped out and back in at the same time, sustained, so the system spends its time moving memory instead of working. A full swap with little movement is not Thrashing.
_Avoid_: swap storm, heavy swapping

**Sustained**:
A condition holding for the whole advisor window (5 minutes by default, configurable) before it produces Advice.
_Avoid_: persistent, prolonged

## History

**History**:
The 24-hour record of samples kept on disk so it survives restarts and crashes.
_Avoid_: log, archive

**Replay**:
Viewing History for a past period, such as the minutes before a freeze, including when each piece of Advice started and ended.
_Avoid_: playback, time travel, rewind

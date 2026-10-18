# History is kept on disk so the lead-up to a crash survives

The spec kept history in memory only, but the main question this tool must answer is "what was happening right before the machine froze or rebooted", which an in-memory buffer loses. So 24 hours of 10-second samples are written to a fixed-size, preallocated ring file that is overwritten in place (a few MB, never grows). The buffer is flushed every 30 seconds, and immediately when memory pressure spikes, so at most ~30 seconds is lost in a hard crash. That is a few MB of writes per day, negligible next to swap traffic, which was the wear concern. Full 1-second detail stays in RAM only.

History stores full process command lines, even though these can contain secrets passed as arguments, because they are what makes a replay useful. The file is readable by root only.

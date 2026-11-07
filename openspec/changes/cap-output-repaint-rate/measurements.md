# Measurements

Workload: `ARGUS_PAINT_WORKLOAD=1 go test ./internal/tui -run TestWorkload_OutputRepaintRate -v`. One Tasks agent pane, 200x50 SimulationScreen, 50 writes/s of ~140-byte lines for 4s, a keystroke echo every 500ms, 3 runs. CPU is process user+system time. Simulation has no ioctl or terminal write, so this is the draw/diff cost only.

## Before (master at 642da57b)

| run | frames | fps | CPU % of one core | echo ms (7 samples) |
| --- | --- | --- | --- | --- |
| 0 | 201 | 50.2 | 10.9 | 1.9–2.5 |
| 1 | 201 | 50.2 | 11.0 | 1.9–2.3 |
| 2 | 201 | 50.2 | 10.4 | 1.7–2.9 |

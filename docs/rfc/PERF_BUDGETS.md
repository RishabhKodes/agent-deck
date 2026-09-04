# Performance budget adjustments

## 2026-09-04: macOS CLI cold-start budget

The `TestPerf_ColdStart_Help` and `TestPerf_ColdStart_Version` base remains 8 ms
on Linux, preserving the existing 40 ms local and 80 ms CI gates. On Darwin,
the base is now 28 ms, yielding a 140 ms local gate through `ColdBudget` (and
280 ms when CI explicitly uses `PERF_BUDGET_MULTIPLIER=2`).

This platform-specific adjustment is necessary because a dynamically linked Go
process on an otherwise idle Apple Silicon host has a measured 47–50 ms launch
floor for this binary. During the repository's default-parallel `go test ./...`
run, competing package tests raised the n=11 trimmed mean to 88.8–121.9 ms even
after help/version were moved ahead of config reads and tmux probes. The old
40 ms budget, and the first 80 ms Darwin allowance, therefore failed without a
code-path regression.

The 140 ms upper bound leaves roughly 15% headroom above the highest observed
parallel trimmed mean while still catching large startup regressions. The
dedicated performance job remains the authoritative low-contention comparison,
and the Linux budget is unchanged.

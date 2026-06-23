# Koordinator Scheduler Benchmark

A lightweight scheduler benchmark tool for `koord-scheduler` that runs in
GitHub Actions CI or local dev, producing reproducible throughput and
latency metrics.

## Quick Start

### Prerequisites
- Go 1.21+
- Docker
- [kind](https://kind.sigs.k8s.io/)
- [kwok](https://github.com/kubernetes-sigs/kwok)

### Run locally

```bash
# From the koordinator repo root:

# 1. Create a local cluster with Koordinator installed
make -C test/perf setup

# 2. Run the basic benchmark scenario
make -C test/perf benchmark

# 3. Tear down the cluster when done
make -C test/perf teardown
```

### Run a specific scenario

```bash
make -C test/perf benchmark SCENARIO=basic
```

### Output

Results are written to `results/result.json` and a summary is printed to stdout:
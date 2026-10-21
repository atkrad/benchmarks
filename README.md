# benchmarks

This repository holds Go runtime experiments that you can rebuild and re-run.
Nix pins the compilers and tools. Go modules pin the libraries. Each experiment
runs on the host, outside the Nix build sandbox, because the measurement is of
a real process.

The first experiment measures how idle HTTP/2 connections affect server memory
and goroutine counts. See
[experiments/http2-idle/README.md](experiments/http2-idle/README.md).

## Requirements

- Linux on x86_64. Other platforms are not set up.
- Nix with flakes enabled. The flake supplies Go, golangci-lint, and benchstat.
  You do not install those by hand.

To enable flakes, add this to `~/.config/nix/nix.conf` or `/etc/nix/nix.conf`
and restart the Nix daemon:

```text
experimental-features = nix-command flakes
```

direnv is optional. It enters the development shell when you `cd` into the
repository. Install `direnv` and
[nix-direnv](https://github.com/nix-community/nix-direnv), hook direnv into
your shell once, then run `direnv allow` here. `.envrc` contains only
`use flake`.

## Development shell

```sh
nix develop
# or, with direnv configured:
direnv allow
```

The shell gives you Go 1.27.2, golangci-lint built with that compiler,
benchstat, git, and a C compiler. `go test -race` needs the C compiler, so the
shell sets `CGO_ENABLED=1`. The first `go test` fetches modules into the
module cache.

## Automated checks

These commands build, format, lint, and test. They do not run the memory
experiment.

```sh
nix run .#test
nix run .#test-integration
nix run .#test-race
nix run .#lint
nix run .#vet
nix run .#fmt
nix run .#fmt-check
nix flake check
```

`fmt` rewrites Go sources. `fmt-check` only prints a diff and fails when one
exists. CI runs `fmt-check`, not `fmt`.

`nix build` and `nix flake check` are hermetic. They use the vendored module
hash. Integration tests open a few local connections to check behavior; they
are not a benchmark. Unit tests do not open sockets, so they can run inside
the Nix sandbox.

## Experiments

| Benchmark | What it measures | Metrics | Run |
| --- | --- | --- | --- |
| [HTTP/2 idle connections](experiments/http2-idle/README.md) | Server goroutines and resident memory while N HTTP/2 connections sit idle, for Go 1.27.2 and a pinned Go 1.28 development commit | RSS, heap, stacks, goroutines, GC | `nix run .#http2-idle -- --out ./local-results` |

```sh
nix build .#http2-idle
nix run .#http2-idle -- --help
nix run .#http2-idle -- --counts 0,1000,5000 --rounds 10 --out ./local-results
```

Results land under `./local-results/<experiment>/<run-id>/`. Each experiment
README describes the tree and how to compare cases with benchstat.

## How to read a result

Nix fixes the compilers, the source, and the tool versions. It does not fix
the kernel, other processes on the machine, or the scheduler. Two runs on
different hosts can disagree. Treat a single run as one observation of that
host.

RSS is the host's view of the process. The Go heap is the runtime's view.
They move for different reasons. A drop in one does not imply a drop in the
other.

benchstat compares independent trials. It does not remove a slow drift that
affects every case, and it does not know which part of RSS belongs to the
change you care about. Subtracting the zero-connection median is a
description, not proof of a cause.

## Cite a commit

Name the Git commit you built (`git rev-parse HEAD`). The run writes the same
revision into `environment.json`. If the tree was dirty, the file says so. A
dirty tree is not a citation.

In a write-up, link the commit on GitHub and quote the `go128_rev` and
`source_rev` fields from that run's `environment.json`.

## Add an experiment

Put it under `experiments/<name>/` with its own README, config, and programs.
Add one row to the table above.

Shared code lives in `internal/benchkit`: process control, procfs reads, trial
order, stability polling, and JSON or benchfmt output. An experiment should
not grow a second copy of those.

Use `testing.B` when the thing you measure is a function call in one process
and the result is allocations or time per operation. Use an external runner,
like this repository does, when the measurement is the process itself:
resident memory, file descriptors, or goroutines that must not include the
load generator.

## License

Apache License 2.0. See [LICENSE](LICENSE).

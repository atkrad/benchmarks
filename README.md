# benchmarks

Go runtime experiments you can rebuild and run again. Nix pins the compilers
and tools. Go modules pin the libraries. An experiment runs on the host,
outside the Nix build sandbox, because the measurement is of a real process.

## Experiments

| Benchmark | What it measures | Metrics | Run |
| --- | --- | --- | --- |
| [HTTP/2 idle connections](experiments/http2-idle/README.md) | Server goroutines and resident memory while N HTTP/2 connections sit idle, for Go 1.27.2 and a pinned Go 1.28 development commit | RSS, heap, stacks, goroutines, GC | `nix run .#http2-idle -- --out ./local-results` |

That README has the configurations, the trial steps, and how to read the
output. This file covers the repository.

## Quick start

```sh
nix develop
nix run .#http2-idle -- --help
nix run .#http2-idle -- --counts 0,1000,5000 --rounds 10 --out ./local-results
```

`--out` is required. Results are written to
`./local-results/http2-idle/<run-id>/`.

## Requirements

- Linux on x86_64. Other platforms are not set up.
- Nix with flakes enabled.

The flake supplies Go 1.27.2, golangci-lint built with that compiler,
benchstat, git, and a C compiler. You do not install those by hand.

To enable flakes, add this to `~/.config/nix/nix.conf` or `/etc/nix/nix.conf`
and restart the Nix daemon:

```text
experimental-features = nix-command flakes
```

```sh
nix develop
```

direnv is optional. Install `direnv` and
[nix-direnv](https://github.com/nix-community/nix-direnv), hook direnv into
your shell once, then run `direnv allow` here. `.envrc` contains only
`use flake`.

The shell sets `CGO_ENABLED=1` because `go test -race` needs a C compiler,
and `GOTOOLCHAIN=local` so Go does not download another toolchain. The first
`go test` fetches modules into the module cache. Entering the shell also
installs the Git pre-commit hook.

## Testing

These commands build, format, lint, and test. They do not run an experiment.

```sh
nix run .#fmt
nix run .#fmt-check
nix run .#lint
nix run .#vet
nix run .#test
nix run .#test-integration
nix run .#test-race
nix run .#pre-commit
nix flake check
```

`fmt` rewrites Go sources. `fmt-check` prints a diff and fails when one
exists. CI runs `fmt-check`, not `fmt`.

`nix run .#test` runs `go test` on the working tree. `nix flake check` builds
the packages in `checks` and runs each package's unit tests inside the Nix
sandbox. Integration tests open a few local connections, so they stay on
`nix run .#test-integration` and out of the sandbox.

`nix run .#pre-commit` runs the hooks the development shell installs: Go
formatting with the pinned golangci-lint, merge-conflict markers, trailing
whitespace, and a final newline. It does not run the tests. The formatter
needs the module cache, so these hooks are not a sandboxed flake check. CI
still runs formatting, lint, vet, unit tests, integration tests, the race
detector, and `nix flake check`.

## Results

Nix pins the compilers, the source, and the tools. It does not pin the
kernel, other processes, or CPU frequency. Two runs on different hosts can
disagree. Publish `environment.json` next to any number you quote.

RSS is the kernel's view of the process. The Go heap is the runtime's view.
They can move independently. benchstat compares trials. It does not remove a
drift that hits every case. The experiment README says how to read its own
summary numbers.

To cite a run, record `git rev-parse HEAD`. The same revision is in
`environment.json`: `go128_rev` is the Go development commit, and
`host.source_rev` is the revision of this repository. `host.source_dirty` is true when the
tree had uncommitted changes. A dirty tree is not a citation. Link the commit
on GitHub.

## Add an experiment

Put it under `experiments/<name>/` with its own README, config, and programs.
Add one row to the table above.

Shared code lives in `internal/benchkit`: process control, procfs reads, trial
order, stability polling, and JSON or benchfmt output. An experiment should
not grow a second copy of those.

Use `testing.B` when you measure a function call in one process and the
result is allocations or time per operation. Use an external runner, as this
repository does for HTTP/2, when the measurement is the process itself:
resident memory, file descriptors, or goroutines that must not include the
load generator.

## License

Apache License 2.0. See [LICENSE](LICENSE).

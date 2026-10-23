# HTTP/2 idle connections

How many server goroutines, and how much resident memory, does one idle HTTP/2
connection cost?

## Configurations

Go 1.28 development changed the HTTP/2 server so a connection with nothing to
write can drop the goroutine that used to sit in the serve loop. A temporary
setting, `GODEBUG=http2serveparking=0`, turns that off. The setting is not
a promise of the final Go 1.28 release.
[Issue 81524](https://github.com/golang/go/issues/81524) says it should be
removed before the release. This experiment uses one commit where the setting
exists: `5c51011e82ed754af072baaf4338ff35e76fe705`. Later commits on that
issue (`0ac31b6`, `ffd88e1`, `43c2090`) are not included. The compiler package
checks that this source still contains `#http2serveparking`. If that check
fails, the build stops.

The server program is the same source in every case. There are two binaries.
Only the compiler, and one environment variable, change.

| Label | Compiler | Runtime |
| --- | --- | --- |
| `go127` | Go 1.27.2 | no `http2serveparking` setting |
| `go128` | the pinned development commit | parking left on |
| `go128off` | the same `go128` binary | `GODEBUG=http2serveparking=0` |

The client and the runner are built with Go 1.27.2. Their memory is not part
of the server measurement. Each trial starts a new server process.

## Methodology

1. The server listens on `127.0.0.1` only. HTTPS negotiates HTTP/2. A second,
   plaintext listener on `127.0.0.1` serves `/health` and `/stats`. Those
   requests are not workload connections.
2. The client is another process. For each of N connections it builds its own
   HTTP transport, trusts the server's certificate, and checks that the
   response is HTTP/2 with ALPN `h2`. `ForceAttemptHTTP2` is on, because a
   custom TLS config would otherwise skip HTTP/2. `SendPingTimeout` is 0, so
   the connection is not kept awake by pings.
3. The runner waits until the server reports exactly N open connections. That
   is the readiness barrier, not the stability check.
4. It then polls until the connection count stays put, the goroutine count
   stays equal, and the RSS span stays inside the configured drift, for the
   configured number of polls. RSS can still move after the goroutine count
   has stopped. The polls are kept, including the ones that did not settle.
5. It records several samples. Each raw observation is appended to
   `samples.jsonl` unchanged. For every reported metric (RSS, heap, stacks,
   goroutines), the trial stores the median of that sample window. The
   primary path does not call `runtime.GC` or `debug.FreeOSMemory`. `/stats`
   does call `runtime.ReadMemStats`, which is the same overhead in every case.
6. A smoke run saves a goroutine profile at the largest connection count in
   the first round, while those connections are still idle.
7. The client sends one more request on each held transport. The request must
   reuse the same connection. The server must still show N accepted
   connections, each with two requests, and no HTTP/1 workload request.
8. The runner closes the client and the server and waits until both are gone.

A failed attempt is written to `trials.jsonl` and, if retries remain, tried
again. A cell that never succeeds makes the run exit non-zero. The runner
does not delete the failure. Failed attempts are not written to the benchfmt
files.

Before the plan, a short probe holds 16 connections on `go128` and
`go128off`. Parking should make the `go128` goroutine growth smaller. If it
does not, the run stops. The probe is not part of the benchstat files.

The default counts are 0, 1000, and 5000, with 10 rounds. The zero count is
the baseline. The runner checks `ulimit -n` and stops with a message if there
are not enough file descriptors. It does not raise the limit.

`--gc-policy forced` calls `POST /gc` before the stability window, on every
case. Do not compare a forced run with a natural run. The default policy is
`natural`.

`--smoke` is a short manual run. It always uses counts 0 and 200 and 3 rounds,
including when other count or round flags are set. Smoke output is not a
result to publish.

## Run

```sh
nix build .#http2-idle
nix run .#http2-idle -- --help
nix run .#http2-idle -- --counts 0,1000,5000 --rounds 10 --out ./local-results
nix run .#http2-idle -- --smoke --out ./local-smoke
```

Flags that you set replace the defaults in `config/default.json`. Flags that
you leave out keep the file's value. `--help` lists the rest.

`--dial-retries` is how many times one connection dial may be repeated.
`--max-trial-retries` is how many times a whole failed trial may be repeated.
The run directory name is created exclusively, so a second run in the same
second does not append to the first.

The Nix app also passes the server binaries, the client, the expected
`runtime.Version` strings, the Go commit, and the nixpkgs revision. You do
not set those by hand.

## Results

```text
./local-results/http2-idle/<run-id>/
├── environment.json
├── config.json
├── plan.json
├── trials.jsonl
├── samples.jsonl
├── summary.json
├── benchfmt/
│   ├── go127.txt
│   ├── go128.txt
│   └── go128off.txt
└── diagnostics/          # smoke runs only
```

Replace `<run-id>` with the directory name the runner created under
`http2-idle/`.

`samples.jsonl` is one raw observation per line. A successful trial stores
the median of that sample window in `trials.jsonl` and in one benchfmt row.
An even-sized window can leave `rss_bytes` and `goroutines` on a half unit.
Failed attempts stay in `trials.jsonl` only.

`summary.json` has per-case medians and ranges, and a baseline-adjusted RSS
(the case's median minus its own zero-connection median). That subtraction is
labeled. It is not a causal estimate. The `benchstat` field is the comparison
command relative to the run directory.

```sh
benchstat \
  -ignore case,go-version,godebug,server-sha256 \
  ./local-results/http2-idle/<run-id>/benchfmt/go127.txt \
  ./local-results/http2-idle/<run-id>/benchfmt/go128.txt \
  ./local-results/http2-idle/<run-id>/benchfmt/go128off.txt
```

From inside the run directory:

```sh
benchstat -ignore case,go-version,godebug,server-sha256 \
  benchfmt/go127.txt benchfmt/go128.txt benchfmt/go128off.txt
```

Each benchfmt file has one row per successful trial. Goroutine counts are
marked `assume=exact`, because an idle server with N held connections should
report the same count. If they vary, benchstat warns. Memory units keep the
default noisy assumption. File keys that differ per case (`case`,
`go-version`, `godebug`, `server-sha256`) are ignored, or benchstat would put
each case in its own table. Keys that should match, such as CPU and GC
policy, are checked and must match.

A `~`, or a note that more samples are needed, means the comparison is
inconclusive. It does not mean the run failed.

A smoke run writes `diagnostics/<case>-n<count>.txt`, the raw `debug=1`
profile, and a `.summary.json` next to it. `debug=1` prints each shared stack
once, with how many goroutines use it. The summary adds those counts. A stack
that contains both `serveLoop` and `serve` is one group. The function names
can change between Go versions. The profile is not the RSS or goroutine
series in the benchfmt files.

If parking works, `go128` should use fewer goroutines per idle connection
than `go127` and `go128off`. Resident memory may follow, because each parked
goroutine has a stack, but RSS also includes the heap, thread stacks, and
memory the kernel has not taken back. Heap totals and RSS do not have to
move together. This document does not predict a number.

## What fails the run

The runner stops the cell, and records why, when any of these happen:

- the server's `runtime.Version` is not the version that was requested
- the probe does not see a smaller goroutine delta with parking on
- the open, accepted, or request counts are not exactly N (and 2N requests
  after the recheck)
- a response is not HTTP/2, or ALPN is not `h2`
- a recheck replaces a connection
- a non-HTTP/2 workload request or a write error is counted
- RSS is missing from `/proc`, or a sample is too far in time from the
  server's clock
- the stability window expires
- the process exits early, or the file-descriptor limit is too low
- a smoke profile cannot be parsed

## Limits

Both server binaries run the same program, with the same `GOMAXPROCS`,
`GOGC`, and CGO disabled. The development commit is not Go 1.28 as it will
ship. Do not swap in a release candidate and keep the `go128` label.

Nix pins the compilers, the source, and the tools. It does not pin the
kernel, the other processes on the machine, or CPU frequency. Publish the
`environment.json` next to any number you quote, and name the Git commit.
See the root [README](../../README.md) for how to cite it.

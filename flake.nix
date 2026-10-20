{
  description = "Reproducible Go benchmarks";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/26f4107e47e3ad0ffd0db220469ba83b6f6d96e8";

  outputs =
    { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs {
        inherit system;
        overlays = [ (import ./nix/overlays/default.nix) ];
      };
      sourceRev = if self ? rev then self.rev else (self.dirtyRev or "unknown");
      http2 = pkgs.callPackage ./nix/packages/http2-idle.nix {
        src = self;
        inherit sourceRev;
        sourceDirty = !(self ? rev);
        nixpkgsRev = nixpkgs.rev;
        nixpkgsHash = nixpkgs.narHash;
        go127 = pkgs.benchmarks.go_1_27_2;
        go128 = pkgs.benchmarks.go_1_28_dev;
        benchstat = pkgs.benchmarks.benchstat;
      };
      goTools = [
        pkgs.benchmarks.go_1_27_2
        pkgs.benchmarks.golangci-lint
        pkgs.stdenv.cc
        pkgs.jq
      ];
      mkTask =
        name: body:
        pkgs.writeShellApplication {
          inherit name;
          runtimeInputs = goTools;
          text = ''
            export CGO_ENABLED=1
            export GOTOOLCHAIN=local
            ${body}
          '';
        };
      # Fail the task when go test reports a skip. A skip is not a pass.
      skipGuard = args: ''
        tmp=$(mktemp)
        trap 'rm -f "$tmp"' EXIT
        set -o pipefail
        go test -json ${args} "$@" | tee "$tmp"
        # Packages with no test files also emit Action=skip with a null Test.
        # Only real t.Skip calls have a non-empty Test name.
        skips=$(jq -r 'select(.Action == "skip" and (.Test // "") != "") | "\(.Package) \(.Test)"' "$tmp")
        if [ -n "$skips" ]; then
          echo "unexpected skipped tests:"
          printf '%s\n' "$skips"
          exit 1
        fi
      '';
      taskBins = {
        test = mkTask "benchmarks-test" (skipGuard "-count=1 ./...");
        test-integration = mkTask "benchmarks-test-integration" (
          skipGuard "-count=1 -tags integration ./..."
        );
        test-race = mkTask "benchmarks-test-race" (
          skipGuard "-race -count=1 -tags integration ./..."
        );
        lint = mkTask "benchmarks-lint" ''
          golangci-lint run ./...
        '';
        vet = mkTask "benchmarks-vet" ''
          go vet ./...
          go vet -tags integration ./...
        '';
        fmt = mkTask "benchmarks-fmt" ''
          golangci-lint fmt
        '';
        fmt-check = mkTask "benchmarks-fmt-check" ''
          diff=$(golangci-lint fmt --diff)
          if [ -n "$diff" ]; then
            printf '%s\n' "$diff"
            exit 1
          fi
        '';
      };
      mkApp = pkg: executable: {
        type = "app";
        program = "${pkg}/bin/${executable}";
      };
    in
    {
      packages.${system} = {
        go-1_27_2 = pkgs.benchmarks.go_1_27_2;
        go-1_28-dev = pkgs.benchmarks.go_1_28_dev;
        benchstat = pkgs.benchmarks.benchstat;
        golangci-lint = pkgs.benchmarks.golangci-lint;
        server-go127 = http2.server-go127;
        server-go128 = http2.server-go128;
        http2-idle-client = http2.client;
        http2-idle-runner = http2.runner;
        http2-idle = http2.http2-idle;
        default = http2.http2-idle;
      } // taskBins;

      apps.${system} = {
        http2-idle = {
          type = "app";
          program = "${http2.http2-idle}/bin/http2-idle";
        };
        test = mkApp taskBins.test "benchmarks-test";
        test-integration = mkApp taskBins.test-integration "benchmarks-test-integration";
        test-race = mkApp taskBins.test-race "benchmarks-test-race";
        lint = mkApp taskBins.lint "benchmarks-lint";
        vet = mkApp taskBins.vet "benchmarks-vet";
        fmt = mkApp taskBins.fmt "benchmarks-fmt";
        fmt-check = mkApp taskBins.fmt-check "benchmarks-fmt-check";
      };

      checks.${system} = {
        server-go127 = http2.server-go127;
        server-go128 = http2.server-go128;
        http2-idle-client = http2.client;
        http2-idle-runner = http2.runner;
        http2-idle = http2.http2-idle;
        benchstat-compare = http2.benchstat-compare;
      };

      # The C compiler comes from the stdenv. CGO_ENABLED=1 is what
      # `go test -race` needs. GOTOOLCHAIN=local refuses to download another Go.
      devShells.${system}.default = pkgs.mkShell {
        packages = [
          pkgs.benchmarks.go_1_27_2
          pkgs.benchmarks.golangci-lint
          pkgs.benchmarks.benchstat
          pkgs.git
          pkgs.jq
        ];
        env = {
          CGO_ENABLED = "1";
          GOTOOLCHAIN = "local";
          BENCHMARKS_SOURCE_REV = sourceRev;
        };
      };
    };
}

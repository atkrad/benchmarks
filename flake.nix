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
    in
    {
      packages.${system} = {
        go-1_27_2 = pkgs.benchmarks.go_1_27_2;
        go-1_28-dev = pkgs.benchmarks.go_1_28_dev;
        benchstat = pkgs.benchmarks.benchstat;
        server-go127 = http2.server-go127;
        server-go128 = http2.server-go128;
        http2-idle-client = http2.client;
        http2-idle-runner = http2.runner;
        http2-idle = http2.http2-idle;
        default = http2.http2-idle;
      };

      apps.${system}.http2-idle = {
        type = "app";
        program = "${http2.http2-idle}/bin/http2-idle";
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

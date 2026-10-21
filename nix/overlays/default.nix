# Scoped toolchain overlay.
#
# These attributes live under pkgs.benchmarks so the overlay does not replace
# pkgs.go. Other tools keep the nixpkgs Go compiler.
final: prev: {
  benchmarks = {
    go_1_27_2 = final.callPackage ../packages/go-1.27.2.nix { };
    go_1_28_dev = final.callPackage ../packages/go-1.28-dev.nix { };
    benchstat = final.callPackage ../packages/benchstat.nix { };
    golangci-lint = final.callPackage ../packages/golangci-lint.nix { };
  };
}

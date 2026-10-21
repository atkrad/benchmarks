# Packages for the HTTP/2 idle experiment.
#
# The server is built twice, once with each compiler. The client and the
# runner stay on Go 1.27.2 so their code does not change between cases.
# Tests run in the sandbox. The memory experiment does not.
{
  lib,
  buildGoModule,
  runCommand,
  writeShellApplication,
  src,
  sourceRev,
  sourceDirty,
  nixpkgsRev,
  nixpkgsHash,
  go127,
  go128,
  benchstat,
}:
let
  # runtime.Version() for the pinned development commit. go-1.28-dev.nix
  # writes this same string into the compiler's VERSION file.
  expectGo128 = "devel go1.28-5c51011e82ed754af072baaf4338ff35e76fe705";
  go128Rev = "5c51011e82ed754af072baaf4338ff35e76fe705";

  mk =
    {
      go,
      pname,
      subPackages,
      doCheck ? false,
      postCheck ? "",
      postInstall ? "",
    }:
    (buildGoModule.override { inherit go; }) {
      inherit
        pname
        src
        subPackages
        doCheck
        postCheck
        postInstall
        ;
      version = sourceRev;
      vendorHash = "sha256-jPpwdR05f8C83zi0I25HBMEbjO4TVc7xV1lFnNt4/38=";
      env = {
        CGO_ENABLED = "0";
        GOTOOLCHAIN = "local";
      };
      # The default check only tests the command package, which has no tests.
      # Run the module tests. Integration tests stay out of the sandbox.
      checkPhase = ''
        runHook preCheck
        go test -count=1 ./...
        runHook postCheck
      '';
    };

  server-go127 = mk {
    go = go127;
    pname = "http2-idle-server-go127";
    subPackages = [ "experiments/http2-idle/cmd/server" ];
  };
  server-go128 = mk {
    go = go128;
    pname = "http2-idle-server-go128";
    subPackages = [ "experiments/http2-idle/cmd/server" ];
    # Compile and test the module with the development compiler.
    doCheck = true;
  };
  client = mk {
    go = go127;
    pname = "http2-idle-client";
    subPackages = [ "experiments/http2-idle/cmd/client" ];
  };
  runner = mk {
    go = go127;
    pname = "http2-idle-runner";
    subPackages = [ "experiments/http2-idle/cmd/runner" ];
    doCheck = true;
    postInstall = ''
      mkdir -p $out/share/http2-idle
      cp experiments/http2-idle/config/default.json $out/share/http2-idle/default.json
    '';
    postCheck = ''
      export GOFLAGS="''${GOFLAGS:-} -mod=vendor"
      go vet ./...
      go vet -tags integration ./...
    '';
  };

  http2-idle = writeShellApplication {
    name = "http2-idle";
    text = ''
      exec ${runner}/bin/runner \
        --config ${runner}/share/http2-idle/default.json \
        --server-go127 ${server-go127}/bin/server \
        --server-go128 ${server-go128}/bin/server \
        --client ${client}/bin/client \
        --expect-go127 go1.27.2 \
        --expect-go128 ${lib.escapeShellArg expectGo128} \
        --go128-rev ${lib.escapeShellArg go128Rev} \
        --nixpkgs-rev ${lib.escapeShellArg nixpkgsRev} \
        --nixpkgs-hash ${lib.escapeShellArg nixpkgsHash} \
        --source-rev ${lib.escapeShellArg sourceRev} \
        ${lib.optionalString sourceDirty "--source-dirty"} \
        "$@"
    '';
  };

  # Confirms benchstat lays the three cases out as columns. The numbers are
  # synthetic. A p-value is not required.
  benchstat-compare = runCommand "http2-idle-benchstat-compare" { nativeBuildInputs = [ benchstat ]; } ''
    report=$(benchstat -ignore case,go-version,godebug,server-sha256 \
      ${src}/internal/benchkit/report/testdata/benchfmt/go127.txt \
      ${src}/internal/benchkit/report/testdata/benchfmt/go128.txt \
      ${src}/internal/benchkit/report/testdata/benchfmt/go128off.txt)
    printf '%s\n' "$report"
    printf '%s\n' "$report" | grep -q 'rss-B'
    printf '%s\n' "$report" | grep -q 'goroutines'
    printf '%s\n' "$report" | grep -q 'vs base'
    printf '%s\n' "$report" | grep -q 'go127.txt'
    printf '%s\n' "$report" | grep -q 'go128.txt'
    printf '%s\n' "$report" | grep -q 'go128off.txt'
    printf '%s\n' "$report" | grep -q -- '-20.00%'
    if printf '%s\n' "$report" | grep -q 'inconsistent'; then
      echo "benchstat split the cases into different tables"
      exit 1
    fi
    mkdir -p $out
    printf '%s\n' "$report" > $out/benchstat.txt
  '';
in
{
  inherit
    server-go127
    server-go128
    client
    runner
    http2-idle
    benchstat-compare
    ;
}

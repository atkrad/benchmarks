# Go at a fixed development commit, not a branch tip.
#
# Commit 5c51011e82ed754af072baaf4338ff35e76fe705 adds server-side HTTP/2
# serve-loop parking and the temporary GODEBUG key #http2serveparking.
# That commit's src/cmd/dist requires a bootstrap compiler of go1.26.0 or
# newer, so this derivation bootstraps with the pinned nixpkgs Go (1.26.x),
# not with the Go 1.24 bootstrap that nixpkgs uses for Go 1.27.
#
# The nixpkgs Go 1.27 patches (zoneinfo, mime, dynamic linker) are kept.
# If they stop applying to this commit, the build fails closed instead of
# silently dropping them.
{
  lib,
  fetchFromGitHub,
  go,
  go_1_27,
}:
let
  rev = "5c51011e82ed754af072baaf4338ff35e76fe705";
  # The full commit is in the version string so `go version` can be checked
  # against the pin. A short prefix would also match a different commit.
  versionLine = "devel go1.28-${rev}";
in
assert lib.assertMsg (
  lib.versionAtLeast go.version "1.26.0"
) "bootstrap Go is ${go.version}; commit ${rev} requires go1.26.0 or newer";
go_1_27.overrideAttrs (old: {
  pname = "go";
  version = "1.28-${builtins.substring 0 12 rev}";

  src = fetchFromGitHub {
    owner = "golang";
    repo = "go";
    inherit rev;
    hash = "sha256-c6B1CX7JTbPnR7OVAVT4zdHuvlV6N+QIwCRenC9kPs8=";
  };

  env = old.env // {
    GOROOT_BOOTSTRAP = "${go}/share/go";
  };

  # The commit has no VERSION file. The first line becomes runtime.Version().
  postPatch = (old.postPatch or "") + ''
    printf '%s\n' '${versionLine}' > VERSION
  '';

  disallowedReferences = (old.disallowedReferences or [ ]) ++ [ go ];

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    version="$($out/bin/go version)"
    echo "go version: $version"
    if ! printf '%s\n' "$version" | grep -q '${rev}'; then
      echo "go version does not contain ${rev}: $version" >&2
      exit 1
    fi
    if ! grep -q '#http2serveparking' "$out/share/go/src/net/http/internal/http2/server.go"; then
      echo "installed Go source is missing the #http2serveparking knob" >&2
      exit 1
    fi
    runHook postInstallCheck
  '';

  meta = (old.meta or { }) // {
    description = "Go compiler at ${rev} (HTTP/2 serve parking development commit)";
    homepage = "https://go.googlesource.com/go/+/${rev}";
  };
})

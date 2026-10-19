# benchstat CLI from a pinned golang.org/x/perf revision.
# This is a tool. Application code does not import it.
{
  buildGoModule,
  fetchFromGitHub,
  go_1_27,
}:
(buildGoModule.override { go = go_1_27; }) {
  pname = "benchstat";
  version = "0.0.0-20261009192801-be2c69fb417e";

  src = fetchFromGitHub {
    owner = "golang";
    repo = "perf";
    rev = "be2c69fb417e05de5fec05c732911f4c5f172606";
    hash = "sha256-Ura+yZPbWLQDB/R+OTSrUpKZtEjDvpitdpabKgDSm2M=";
  };

  vendorHash = "sha256-caW4rox/r2eq0HxACaAsuDLAeGIKUiLgKtJol8OXzDE=";

  subPackages = [ "cmd/benchstat" ];

  doCheck = false;

  meta = {
    description = "Benchstat compares and summarizes benchmark results";
    homepage = "https://pkg.go.dev/golang.org/x/perf/cmd/benchstat";
    mainProgram = "benchstat";
  };
}

# golangci-lint built with the pinned Go 1.27.2 compiler.
# nixpkgs ships a binary built with an older Go, which cannot lint this module.
{
  buildGoModule,
  fetchFromGitHub,
  go_1_27,
}:
(buildGoModule.override { go = go_1_27; }) {
  pname = "golangci-lint";
  version = "2.15.0";

  src = fetchFromGitHub {
    owner = "golangci";
    repo = "golangci-lint";
    rev = "25c5369a93aacb8cccea0d46e1569ca4a194564d";
    hash = "sha256-m9PoJHescRuT9DiKsb6LL+lQlc7RKAWDiRyf8pNMffA=";
  };

  vendorHash = "sha256-gWbhZc/BJwuiwyTMaR1ShEBI1lo0bGLCXsdj5n91baw=";

  subPackages = [ "cmd/golangci-lint" ];

  doCheck = false;

  meta = {
    description = "Fast linters runner for Go";
    homepage = "https://golangci-lint.run/";
    mainProgram = "golangci-lint";
  };
}

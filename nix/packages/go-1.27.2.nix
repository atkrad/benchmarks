# Exact Go 1.27.2 from the pinned nixpkgs. A newer patch is not a substitute.
{ lib, go_1_27 }:
let
  want = "1.27.2";
in
if go_1_27.version != want then
  throw "pinned nixpkgs provides go_1_27 ${go_1_27.version}, want exactly ${want}"
else
  go_1_27

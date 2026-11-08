{
  description = "llmtop - btop-style terminal monitor for a self-hosted LLM stack";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = function: nixpkgs.lib.genAttrs systems (system: function nixpkgs.legacyPackages.${system});

      mkLlmtop =
        pkgs:
        pkgs.buildGoModule {
          pname = "llmtop";
          version = "0.2.0";
          src = ./.;
          # Changes whenever go.mod or go.sum do: set it to pkgs.lib.fakeHash,
          # build once and paste the hash Nix reports.
          vendorHash = "sha256-uwBJAqN4sIepiiJf9lCDumLqfKJEowQO2tOiSWD3Fig=";
          env.CGO_ENABLED = "0";
          ldflags = [
            "-s"
            "-w"
          ];
          meta = {
            description = "btop-style terminal monitor for a self-hosted LLM stack: engines, GPUs and who is calling";
            license = pkgs.lib.licenses.mit;
            mainProgram = "llmtop";
          };
        };
    in
    {
      packages = forAllSystems (pkgs: {
        llmtop = mkLlmtop pkgs;
        default = mkLlmtop pkgs;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.gcc
          ];
        };
      });
    };
}

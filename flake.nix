{
  description = "llmtop - btop-style TUI for self-hosted LLM inference engines";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = function: nixpkgs.lib.genAttrs systems (system: function nixpkgs.legacyPackages.${system});

      mkLlmtop = pkgs: pkgs.python3.pkgs.buildPythonApplication {
        pname = "llmtop";
        version = "0.1.0";
        pyproject = true;
        src = ./.;
        build-system = [ pkgs.python3.pkgs.setuptools ];
        dependencies = [
          pkgs.python3.pkgs.textual
          pkgs.python3.pkgs.rich
        ];
        nativeCheckInputs = [ pkgs.python3.pkgs.pytest ];
        preCheck = ''
          export PYTHONPATH=$PWD/src''${PYTHONPATH:+:$PYTHONPATH}
        '';
        checkPhase = ''
          runHook preCheck
          pytest tests
          runHook postCheck
        '';
        meta = {
          description = "TUI for local inference engines and the clients hitting them";
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
            (pkgs.python3.withPackages (ps: [
              ps.textual
              ps.rich
              ps.pytest
            ]))
          ];
          shellHook = ''
            export PYTHONPATH=$PWD/src''${PYTHONPATH:+:$PYTHONPATH}
          '';
        };
      });
    };
}

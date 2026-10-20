{
  description = "vesnik - AI-written release notes and version bumps, reviewed before anything ships";

  # CI pushes every build of main and every release to this cache.
  nixConfig = {
    extra-substituters = [ "https://vesnik.cachix.org" ];
    extra-trusted-public-keys = [ "vesnik.cachix.org-1:ZyKRTirZkXT2SCzbX4Jb8wMqti2pb7qx370S/YkqF9k=" ];
  };

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

  outputs =
    { self, nixpkgs }:
    let
      inherit (nixpkgs) lib;

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      overlay = final: prev: {
        vesnik = final.callPackage ./package.nix {
          version = self.shortRev or self.dirtyShortRev or "dev";
        };
      };

      forAllSystems =
        f:
        lib.genAttrs systems (
          system:
          f (
            import nixpkgs {
              inherit system;
              overlays = [ overlay ];
            }
          )
        );
    in
    {
      overlays.default = overlay;

      packages = forAllSystems (pkgs: {
        default = pkgs.vesnik;
        vesnik = pkgs.vesnik;
      });

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = lib.getExe pkgs.vesnik;
        };
      });

      checks = forAllSystems (pkgs: {
        vesnik = pkgs.vesnik;
      });

      # Every development tool comes from here; `just` lists the tasks.
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            goreleaser
            lefthook
            just
            actionlint
            shellcheck
            nixpkgs-fmt
            git
          ];
          shellHook = ''
            if [ -d .git ] && [ ! -f .git/hooks/pre-commit ]; then
              lefthook install >/dev/null
            fi
          '';
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}

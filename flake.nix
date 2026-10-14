{
  description = "chainsniffer";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    # The agent tooling (herdr, beads, openspec, claude, ...) is pinned by devflake's own lock;
    # `nix flake update devflake` upgrades it.
    devflake.url = "github:slwst/devflake";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      devflake,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        # The project's build, e.g. buildGoApplication, buildNpmPackage, crane's buildPackage.
        # packages.default = ...;

        # `nix flake check` is the gate agents run before merging: tests and linters go here,
        # built offline in the Nix sandbox.
        checks = { };

        devShells.default = devflake.lib.mkShell {
          inherit pkgs;
          # herdr session and workspace name
          name = "chainsniffer";
          # The project's language toolchain
          packages = [ ];
        };
      }
    );
}

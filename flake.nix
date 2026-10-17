{
  description = "chainsniffer";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    # The agent tooling (herdr, beads, openspec, claude, ...) is pinned by devflake's own lock;
    # `nix flake update devflake` upgrades it.
    devflake.url = "github:slwst/devflake/v0.1.0";
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

        chainsniffer = pkgs.buildGoModule (finalAttrs: {
          pname = "chainsniffer";
          version = "0.2.0";
          src = ./.;

          # Pinned hash of the vendored Go dependencies. It has to be updated
          # whenever go.mod or go.sum changes, otherwise `nix build` fails with a
          # hash mismatch. To update it:
          #
          #   1. replace the value below with `pkgs.lib.fakeHash`
          #   2. run `nix build` and copy the "got:" hash from the error
          #   3. put that hash back here and re-run `nix build`
          #
          # (`nix build 2>&1 | grep got:` does steps 2 and 3's reading in one go.)
          vendorHash = "sha256-u3NrcZYeld019Zx0RQ8xS/O3uXrVyRYNet5/cecmfus=";

          subPackages = [ "cmd/chainsniffer" ];

          env.CGO_ENABLED = 0;
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${finalAttrs.version}"
          ];

          # buildGoModule would otherwise only test the subPackages above.
          checkPhase = ''
            runHook preCheck
            go test ./...
            runHook postCheck
          '';

          meta = {
            description = "Discord bot relaying in-chain Wanderer killmails";
            mainProgram = "chainsniffer";
          };
        });

        # A check that reuses the package's vendored dependencies and toolchain
        # but runs `command` instead of building the binary, so it stays offline.
        goCheck =
          name:
          {
            nativeBuildInputs ? [ ],
            command,
          }:
          chainsniffer.overrideAttrs (old: {
            pname = "chainsniffer-${name}";
            # Reuse the package's vendor derivation rather than letting the
            # renamed pname above build an identical copy per check.
            goModules = chainsniffer.goModules;
            nativeBuildInputs = old.nativeBuildInputs ++ nativeBuildInputs;
            doCheck = false;
            buildPhase = ''
              runHook preBuild
              ${command}
              runHook postBuild
            '';
            installPhase = ''
              runHook preInstall
              touch $out
              runHook postInstall
            '';
            dontFixup = true;
          });

        # The state directory the relay defaults to (CHAINSNIFFER_STATE_DIR), declared
        # as a volume in the image so a container keeps its state across restarts.
        stateDir = "/var/lib/chainsniffer";

        # Runs as an unprivileged user; 65532 is the conventional "nonroot" uid of
        # distroless-style images, and nothing in the image needs root.
        uid = 65532;

        # A single-binary image: the Go binary, the CA bundle it needs to talk TLS to
        # Wanderer, Discord and ESI, and the state directory. No shell, no package
        # manager, nothing else at runtime.
        image = pkgs.dockerTools.buildLayeredImage {
          name = "chainsniffer";
          tag = chainsniffer.version;
          contents = [ pkgs.cacert ];

          # buildLayeredImage runs this under fakeroot in the image root, which is the
          # only way to own a directory by a uid the build user is not.
          fakeRootCommands = ''
            mkdir -p .${stateDir}
            chown -R ${toString uid}:${toString uid} .${stateDir}
          '';

          config = {
            Entrypoint = [ "${chainsniffer}/bin/chainsniffer" ];
            # Go finds no /etc/ssl/certs in an image this bare, so point it at the bundle.
            Env = [ "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt" ];
            User = "${toString uid}:${toString uid}";
            Volumes = {
              ${stateDir} = { };
            };
          };
        };
      in
      {
        packages = {
          default = chainsniffer;
          inherit image;
        };

        # `nix flake check` is the gate agents run before merging: tests and linters go here,
        # built offline in the Nix sandbox.
        checks = {
          # Builds the binary; buildGoModule's checkPhase runs `go test ./...`.
          build = chainsniffer;

          vet = goCheck "vet" { command = "go vet ./..."; };

          golangci-lint = goCheck "golangci-lint" {
            nativeBuildInputs = [ pkgs.golangci-lint ];
            command = ''
              export GOLANGCI_LINT_CACHE="$TMPDIR/golangci-lint"
              golangci-lint run ./...
            '';
          };

          # Builds the container image, so a broken image definition fails the gate
          # rather than the next `nix build .#image`.
          inherit image;
        };

        devShells.default = devflake.lib.mkShell {
          inherit pkgs;
          # herdr session and workspace name
          name = "chainsniffer";
          # The project's language toolchain
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.golangci-lint
          ];
        };
      }
    );
}

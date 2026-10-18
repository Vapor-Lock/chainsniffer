# Proposal

## Why

The only way to run chainsniffer today is to build the image on a machine with Nix and
`docker load` it on the host, and the README's Compose example says `pull_policy: never` because
there is nowhere to pull from. The `v0.1.0` tag exists but published nothing. A deployment next to
a self-hosted Wanderer should be a short block pasted into the compose file that already runs
Wanderer, followed by `docker compose pull && docker compose up -d`. Upgrading should mean changing
the image tag.

## What Changes

- **Release pipeline:** pushing a `vX.Y.Z` tag runs a GitHub Actions workflow. It checks that the
  tag matches the version in `flake.nix`, runs `nix flake check`, builds the image for
  `linux/amd64` and `linux/arm64`, and publishes it to `ghcr.io/vapor-lock/chainsniffer` as
  `X.Y.Z`, `X.Y` and `latest`. It then creates a GitHub Release whose notes come from the
  version's `CHANGELOG.md` entry.
- **CI gate:** `nix flake check` runs on every pull request and every push to `main`, so a tag
  can only be cut from a commit that already passed it.
- **Changelog:** `CHANGELOG.md` records each release, including the upgrade notes that today are
  scattered through the README (the `components` default card style and the `CHAINSNIFFER_KSPACE=none`
  default). The release fails if the tagged version has no changelog entry.
- **Image metadata:** the image carries OCI labels (`source`, `version`, `revision`, `description`, `licenses`),
  which tie the GHCR package to the repository and let an operator see what is running.
- **Production compose snippet:** `deploy/compose.yaml` is a ready-to-use service definition. It
  pulls the published image, reads its tokens as Compose secrets, takes its non-secret settings
  from `deploy/chainsniffer.env`, keeps its state on a named volume, and runs hardened: read-only
  root filesystem, all capabilities dropped, `no-new-privileges`, and bounded log files. You can
  paste it into an existing Wanderer `docker-compose.yml` or pull it in with a one-line Compose
  `include:`. It defaults to reaching Wanderer over the compose network, so the SSE stream does
  not pass through the public reverse proxy.
- **License:** the project is released under the MIT License. A `LICENSE` file goes in the
  repository root and the image is labelled `MIT`, so the published image and the public
  repository both say what people may do with them.
- **Release procedure:** `RELEASING.md` covers the maintainer steps: bump the version, write the
  changelog entry, merge, tag, push. It also covers the one-time GHCR visibility setting.
- **First release:** version `0.2.0`, the first published image. It covers everything merged since
  `v0.1.0`.
- The README's "Running" section leads with the published image. Building locally with
  `nix build .#image` stays documented as an alternative.

## Capabilities

### New Capabilities

- `release-distribution`: how a version of chainsniffer is cut, published as a container image,
  documented in the changelog, and deployed with Docker Compose.

### Modified Capabilities

- `relay-runtime`: the **Container image** requirement gains version and license metadata and the guarantee
  that the image runs with a read-only root filesystem and no Linux capabilities.

## Impact

- New: `LICENSE` (MIT), `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `CHANGELOG.md`,
  `RELEASING.md`, `deploy/compose.yaml`, `deploy/chainsniffer.env.example`.
- `flake.nix`: image labels, version bumped to `0.2.0`. `vendorHash` does not change because
  `go.mod` does not.
- `README.md`: the Running and Docker Compose sections.
- External: a public GHCR package under the `Vapor-Lock` org, and GitHub Actions minutes on
  hosted `ubuntu-24.04` and `ubuntu-24.04-arm` runners (free for public repositories).
- No Go code changes. The binary already reports its version (`main.version`) at startup.

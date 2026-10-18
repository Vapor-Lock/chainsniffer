# Design

## Context

- `flake.nix` builds the binary (`version = "0.1.0"`, stamped with `-X main.version`) and an image
  with `dockerTools.buildLayeredImage`, tagged with that version. `nix flake check` already builds
  the image as one of its checks.
- The repository is public on GitHub (`Vapor-Lock/chainsniffer`) and has no `.github/` directory,
  so there is no CI. `v0.1.0` is tagged locally and on the remote, but nothing was published for
  it, and 38 commits have landed since. They include two changed defaults that operators must act
  on (`CHAINSNIFFER_CARD_STYLE=components`, `CHAINSNIFFER_KSPACE=none`).
- The relay writes only inside its state directory. That includes its temp files: `state.go`
  uses `os.CreateTemp(dir, …)`. So a read-only root filesystem works with no code change.
- Wanderer listens on `PORT`, which defaults to `8000` in its `config/runtime.exs`. Upstream
  publishes no production compose file, so the operator's Wanderer service name is their own.
  `wanderer` is the common choice.
- The repository has no LICENSE file yet. This change adds the MIT License.

## Goals / Non-Goals

**Goals:**
- A release is one `git tag` and `git push`. Nothing is built on a laptop, and every published
  artifact traces back to a tagged commit that passed the gate.
- The image a release publishes is the same derivation `nix build .#image` builds. There is no
  second Dockerfile to drift from it.
- An operator with a Wanderer compose project goes from nothing to a running relay by copying
  one directory, filling in one env file and two secret files, and adding one line.

**Non-Goals:**
- Binary tarballs, Homebrew, or NixOS modules. The container is the supported deployment.
- Automatic version bumps or commit-message-driven changelogs. Releases are rare and deliberate,
  and a human writes the upgrade notes.
- Image signing and SBOM attestation. Both can be added to the same workflow later without
  changing anything here.
- Auto-updating deployments, such as Watchtower. The snippet pins a version on purpose.

## Decisions

### `flake.nix` holds the version; the tag must match it
The release reads the version with `nix eval --raw .#default.version` and fails unless the tag is
`v` followed by that version. *Alternative:* derive the version from the tag at build time
(`--impure`, or a generated file). Rejected: `nix build` on any checkout would then produce a
different version string than the release did, and the version bump would no longer be a
reviewable change in the PR that also adds the changelog entry.

### Nix builds the image on native runners; skopeo assembles the multi-arch image
One job per platform (`ubuntu-24.04` for amd64, `ubuntu-24.04-arm` for arm64) runs
`nix build .#image` and pushes the result with
`skopeo copy docker-archive:result docker://ghcr.io/vapor-lock/chainsniffer:X.Y.Z-<arch>`. A final
job combines the two with `docker buildx imagetools create` (or `crane index append`) into a
manifest list under each of the release's tags. *Alternatives:* a Dockerfile with
`docker/build-push-action` and QEMU, rejected because it builds a second image that only looks
like the gated one. Nix cross-compiling arm64 on an amd64 runner, rejected because
`buildLayeredImage` for another system needs either remote builders or emulation, and native arm
runners are free for public repositories. The per-arch `X.Y.Z-<arch>` tags stay in place. They
are harmless, and a single platform can be pulled by them when debugging.

### Install Nix with DeterminateSystems' installer and cache through GitHub Actions
`DeterminateSystems/nix-installer-action` with `magic-nix-cache-action` (or
`cachix/install-nix-action` with no cache). The cache only speeds things up and nothing depends
on it. Every third-party action is pinned by commit SHA.

### The gate runs again on the tag
The release job runs `nix flake check` on the tagged commit even though CI already passed it on
`main`. That costs a few minutes and closes the gap of a tag pushed on a commit that never went
through a PR.

### Tag scheme `X.Y.Z`, `X.Y`, `latest`; pre-releases get their full version only
This follows the usual registry convention, so operators can choose how much drift they accept.
The snippet and README recommend pinning the full `X.Y.Z`. Pre-1.0, a minor bump may change
defaults, which is exactly what happened in 0.2.0. The workflow treats a version as a pre-release
if it contains `-`. It also marks the GitHub Release `prerelease` and skips the moving tags.

### `CHANGELOG.md` in Keep a Changelog format; the workflow extracts the entry
Each version gets a `## [X.Y.Z] - YYYY-MM-DD` heading with `### Upgrading`, `### Added`,
`### Changed` and `### Fixed` subsections as needed. An `awk` step extracts the section between
the version's heading and the next `## [` heading, fails if it is empty, and passes it to
`gh release create --notes-file`. The workflow appends a footer naming the image reference.
*Alternative:* GitHub's generated notes from PR titles. Rejected as the only source because PR
titles do not tell an operator what to change in their environment.

### MIT License, stated in three places
`LICENSE` in the repository root holds the standard MIT text, with copyright `2026 Vapor-Lock`. The
image's `org.opencontainers.image.licenses` label is `MIT`, an SPDX identifier, and
`meta.license = lib.licenses.mit` goes on the Nix package, so GitHub, GHCR and Nix tooling all
report the same license. The image ships only the binary and a CA bundle, and the CA bundle is
under MPL-2.0. No third-party license text has to be bundled, because the Go dependencies are
compiled into the binary and are all permissively licensed (checked in task 1.1).

### Image labels set in `flake.nix`
`config.Labels` gets `org.opencontainers.image.source = "https://github.com/Vapor-Lock/chainsniffer"`,
`.version = version`, `.description`, `.licenses = "MIT"`, and `.revision = self.rev or self.dirtyRev or "unknown"`.
GHCR uses the `source` label to link the package to the repository. Including `revision` means
the image derivation changes on every commit. The image is cheap to rebuild and its layers are
cached, so that is acceptable.

### The compose snippet lives in `deploy/` and works pasted or included
`deploy/compose.yaml` defines `services.chainsniffer`, `volumes.chainsniffer-state` and the two
`secrets`, with paths relative to `deploy/`. It runs
`image: ghcr.io/vapor-lock/chainsniffer:X.Y.Z`. Non-secret settings come from
`env_file: chainsniffer.env`, so the operator never edits the snippet itself. They copy
`chainsniffer.env.example` to `chainsniffer.env`. The fastest way in is
`include: [ chainsniffer/compose.yaml ]` (Compose v2.20+). Included services join the including
project's default network, so `http://wanderer:8000` resolves. The README also shows the block to
paste for older Compose versions. *Alternative:* `${VAR}` interpolation from the Wanderer
project's `.env`. Rejected because it mixes chainsniffer's settings into Wanderer's file and
silently accepts a misspelled variable as empty.

Hardening in the snippet: `read_only: true`, `cap_drop: [ALL]`,
`security_opt: [no-new-privileges:true]`, `restart: unless-stopped`, `stop_grace_period: 15s`,
`logging: json-file` with `max-size: 10m` and `max-file: "3"`, and `CHAINSNIFFER_LOG_FORMAT=json`
in the example env. The snippet has no `depends_on: wanderer`. A connection refused while
Wanderer starts is retried with backoff, and a hard dependency would break the paste for anyone
whose service has another name.

### The internal URL is the default
Sending the SSE stream over the compose network avoids reverse-proxy idle timeouts and response
buffering, which are the usual causes of a stalled stream that the staleness watchdog then has to
recover from. The example env says how to switch to the public `https://` URL when chainsniffer
runs on a different host.

## Risks / Trade-offs

- [GHCR makes a new org package private on its first push] → `RELEASING.md` covers the one-time
  step: Package settings → Change visibility → Public, and confirm the package is linked to the
  repository. The workflow ends with an unauthenticated `docker manifest inspect` of the published
  tag, so a private package fails the release loudly instead of failing users quietly.
- [The workflow needs `packages: write` and `contents: write`] → Permissions are declared per
  job, with the read-only default everywhere else. The token is the built-in `GITHUB_TOKEN`, so
  no long-lived secret exists.
- [The operator's Wanderer service is not called `wanderer`, or does not listen on 8000] → The
  failure is a logged connection error that retries, not a crash. The example env and README name
  both assumptions next to the variable.
- [The internal URL bypasses whatever the proxy enforces, such as IP allow-lists] → The stream is
  authenticated by the map API key either way, and the public URL remains a one-line change.
- [A release half-publishes, for example arm64 pushed and the manifest step fails] → Moving tags
  are only written in the final job, after both platforms are pushed. Re-running the failed job is
  idempotent. A broken release is fixed by releasing `X.Y.Z+1`, never by re-pushing `X.Y.Z`.
- [`v0.1.0` exists but has no image] → It stays as it is. The changelog records 0.1.0 as
  unpublished, and `0.2.0` is the first published release.

## Migration Plan

1. Merge this change, which includes the 0.2.0 version bump and changelog entry. CI runs on the PR.
2. Tag `v0.2.0` on the merge commit and push the tag. The release workflow publishes the image.
3. One-time: set the GHCR package public, then re-run the verification step if it failed.
4. Operators who load images with `docker load` switch `image:` to the GHCR reference and drop
   `pull_policy: never`. Their state volume carries over unchanged.

Rollback: change the snippet's `image:` back to the previous version tag. State files are
compatible within 0.x unless a changelog entry says otherwise.

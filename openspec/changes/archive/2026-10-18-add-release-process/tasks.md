# Tasks

## 1. License and image metadata

- [x] 1.1 Add `LICENSE` with the standard MIT text (`Copyright (c) 2026 Vapor-Lock`) and set `meta.license = lib.licenses.mit` on the package in `flake.nix`. Confirm with `go list -m all` and each module's license that no dependency requires bundled notices beyond what MIT/BSD/Apache-2.0 allow in a compiled binary. Verify that GitHub's repository sidebar shows "MIT license" after merge, and that `nix eval .#default.meta.license.spdxId` prints `"MIT"`.
- [x] 1.2 Add `config.Labels` to the image in `flake.nix` (`org.opencontainers.image.source`, `.version`, `.revision` from `self.rev or self.dirtyRev or "unknown"`, `.description`, `.licenses = "MIT"`). Verify that `nix build .#image && docker load < result && docker inspect chainsniffer:<ver> --format '{{json .Config.Labels}}'` shows all five.
- [x] 1.3 Verify the locked-down runtime scenario by hand: `docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges -v cs-test:/var/lib/chainsniffer` with a deliberately bad `CHAINSNIFFER_WANDERER_URL` gets past state-directory setup, then exits on the connection or config error rather than a write error. Record the command in the PR description.
- [x] 1.4 Add a "License" section to the README ("MIT, see LICENSE") and mention the labels in its image section, and confirm `nix flake check` passes.

## 2. Continuous checks

- [x] 2.1 Add `.github/workflows/ci.yml`: triggers on `pull_request` and on `push` to `main`, `permissions: contents: read`, installs Nix (SHA-pinned DeterminateSystems installer and magic-nix-cache), runs `nix flake check -L`. Verify it runs green on the PR that adds it.

## 3. Changelog and version

- [x] 3.1 Create `CHANGELOG.md` in Keep a Changelog format with a `## [0.2.0] - <date>` entry that covers the work since `v0.1.0` (`git log v0.1.0..HEAD`, archived openspec changes). Include an `### Upgrading` section for the `components` default card style, Attach Files for `strip`, and `CHAINSNIFFER_KSPACE=none`. Add a `## [0.1.0]` entry marked unpublished. Verify that the extraction command in 4.2, run locally, prints exactly the 0.2.0 section.
- [x] 3.2 Bump `version` in `flake.nix` to `0.2.0`. Verify `nix eval --raw .#default.version` prints `0.2.0` and the README's `0.1.0` image references are updated.

## 4. Release workflow

- [x] 4.1 Add `.github/workflows/release.yml`, triggered on `push` of tags `v*`. Its `verify` job checks the tag against `nix eval --raw .#default.version` and fails naming both versions, extracts the changelog entry and fails if it is empty (uploading it as an artifact), runs `nix flake check -L`, and outputs `version` and `prerelease` (true if the version contains `-`).
- [x] 4.2 Add the `image` matrix job (`ubuntu-24.04` → `amd64`, `ubuntu-24.04-arm` → `arm64`) with `permissions: packages: write`. It runs `nix build .#image` and `skopeo copy docker-archive:result docker://ghcr.io/vapor-lock/chainsniffer:<ver>-<arch>`, logging in with `GITHUB_TOKEN`.
- [x] 4.3 Add the `publish` job, which needs `image`. It creates the multi-arch manifest under `<ver>`, plus `<major>.<minor>` and `latest` unless it is a pre-release. Then `gh release create v<ver> --notes-file` with the changelog entry and an image-reference footer (`--prerelease` when applicable). Last, run an unauthenticated `docker manifest inspect ghcr.io/vapor-lock/chainsniffer:<ver>` that fails if either platform is missing.
- [x] 4.4 Pin every action by commit SHA, with the version in a trailing comment, and set job-level permissions. Dry-run on a fork, or with a `v0.0.0-rc.1`-style tag whose flake version matches on a throwaway branch, and verify the pre-release path publishes only the full tag. Delete the test package version and release afterwards.

## 5. Compose deployment

- [x] 5.1 Add `deploy/compose.yaml`: the `chainsniffer` service on `ghcr.io/vapor-lock/chainsniffer:0.2.0`, `env_file: chainsniffer.env`, the `_FILE` token variables, both secrets from `./secrets/wanderer.key` and `./secrets/discord.token`, the `chainsniffer-state` volume, and the hardening from design.md (read_only, cap_drop ALL, no-new-privileges, restart, 15s stop grace, json-file log rotation). Verify with `docker compose -f deploy/compose.yaml config`.
- [x] 5.2 Add `deploy/chainsniffer.env.example`. Every non-secret variable from the README appears with its default, the required ones are uncommented, `CHAINSNIFFER_WANDERER_URL=http://wanderer:8000` is annotated with the service-name and port assumption plus the public-URL alternative, and `CHAINSNIFFER_LOG_FORMAT=json` is set. Add `deploy/chainsniffer.env` and `deploy/secrets/` to `.gitignore`. Verify `git check-ignore` covers both.
- [x] 5.3 Verify the include path end to end: a scratch compose project with a stub `wanderer` service (any HTTP server on 8000) and `include: [chainsniffer/compose.yaml]`. `docker compose up` must start chainsniffer on the same network, resolve `wanderer`, and read both secrets with the uid-65532 file-permission note satisfied. `docker inspect` must show no token in the environment.
- [x] 5.4 Rewrite the README "Running" section around the published image: `docker run` with the GHCR reference, then "Docker Compose" with the `include:` route and the full paste-in block, the secret-file permission note, upgrading by bumping the tag, and local `nix build .#image` kept as "Building the image yourself". Remove `pull_policy: never` from the primary example. Verify every command shown matches the files in `deploy/`.

## 6. Release procedure

- [x] 6.1 Write `RELEASING.md`: choose the version (pre-1.0, a minor bump for changed defaults), bump `flake.nix`, add the changelog entry with Upgrading notes, merge through a PR, `git tag -a vX.Y.Z -m "chainsniffer X.Y.Z"` on the merge commit, `git push origin vX.Y.Z`, watch the workflow, and confirm the image pulls anonymously. Also cover the first-release-only GHCR visibility step and "never re-tag; release a patch instead". Link it from the README's Development section.
- [x] 6.2 Integration check: `nix flake check` passes, the CI workflow is green on the PR, and `openspec validate add-release-process --strict` passes.

## Workflow follow-up

- After merge, tag and push `v0.2.0` (needs the maintainer's go-ahead), make the GHCR package public, and confirm `docker pull ghcr.io/vapor-lock/chainsniffer:0.2.0` works unauthenticated on an amd64 and an arm64 host.
- Switch the production Wanderer compose file to the `deploy/` snippet and confirm kills post.
- Archive the change.

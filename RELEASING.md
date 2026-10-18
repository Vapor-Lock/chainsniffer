# Releasing

A release is a git tag. Pushing `vX.Y.Z` runs [`.github/workflows/release.yml`](.github/workflows/release.yml),
which publishes `ghcr.io/vapor-lock/chainsniffer:X.Y.Z` for `linux/amd64` and `linux/arm64` and
creates a GitHub Release from the version's [`CHANGELOG.md`](CHANGELOG.md) entry. Nothing is
built on a laptop.

The workflow publishes nothing unless all three of these hold for the tagged commit:

- the tag is `v` followed by the version in `flake.nix` (`nix eval --raw .#default.version`),
- `CHANGELOG.md` has a non-empty `## [X.Y.Z]` entry,
- `nix flake check` passes.

## 1. Choose the version

The project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0:

- **Minor** (`0.2.0` → `0.3.0`) for new features, and for any change an operator must act on: a
  changed default, a new required permission, a renamed or removed variable.
- **Patch** (`0.2.0` → `0.2.1`) for fixes that need nothing from the operator.

A version with a suffix, such as `0.3.0-rc.1`, is a pre-release. It is published under its full
version only, its GitHub Release is marked pre-release, and `X.Y` and `latest` stay where they are.

## 2. Prepare the release PR

On a branch, in one PR:

1. Set `version` in `flake.nix` to the new version.
2. Add a `## [X.Y.Z] - YYYY-MM-DD` entry at the top of `CHANGELOG.md`, with `### Upgrading`,
   `### Added`, `### Changed` and `### Fixed` subsections as needed, and a link reference at the
   bottom:
   ```
   [X.Y.Z]: https://github.com/Vapor-Lock/chainsniffer/compare/vPREVIOUS...vX.Y.Z
   ```
   **Upgrading** lists everything an operator must change in their environment, with the setting
   that restores the old behaviour. Leave it out only when there is nothing to do.
3. Move the pinned version in `deploy/compose.yaml` and the examples in `README.md` to the new
   version (`grep -rn 'PREVIOUS' README.md deploy/` finds them).
4. Check the result locally:
   ```sh
   nix flake check
   .github/scripts/check-release-version.sh vX.Y.Z   # prints X.Y.Z
   .github/scripts/release-notes.sh X.Y.Z            # prints the entry the release will show
   ```

Merge the PR once CI is green. The release date in the heading is the day you tag; fix it in the
PR if the merge slips.

## 3. Tag and push

Tag the merge commit on `main`, not your branch:

```sh
git switch main
git pull --ff-only
git log -1 --oneline          # the release PR's merge commit
git tag -a vX.Y.Z -m "chainsniffer X.Y.Z"
git push origin vX.Y.Z
```

## 4. Watch the workflow

```sh
gh run list --workflow release.yml --limit 1
gh run watch                  # pick the run for vX.Y.Z
```

The jobs run in order: **Verify** (tag, changelog, gate), **Image** for amd64 and arm64 (pushes
`X.Y.Z-amd64` and `X.Y.Z-arm64`), then **Publish**, which writes the multi-arch `X.Y.Z` (plus `X.Y`
and `latest` for a stable release), creates the GitHub Release, and checks anonymously that the
image is public and has both platforms.

A failed job can be re-run with `gh run rerun --failed`. Every step is safe to repeat, and the
moving tags are written only after both platforms are pushed.

## 5. Confirm the image pulls anonymously

The workflow's last step already checks this, but confirm it from a machine that is not logged in
to GHCR:

```sh
docker logout ghcr.io
docker pull ghcr.io/vapor-lock/chainsniffer:X.Y.Z
docker run --rm ghcr.io/vapor-lock/chainsniffer:X.Y.Z   # exits 2, naming the missing variables
```

Pull on an arm64 host too when the release touches the image.

## First release only: make the package public

GHCR creates a new organisation package as **private**, so the first release's last step fails
with an authentication error. Once, after that first run:

1. Open the package: GitHub → Vapor-Lock → Packages → `chainsniffer`.
2. Package settings → Danger Zone → **Change visibility** → **Public**.
3. Check that the package page names the `Vapor-Lock/chainsniffer` repository. The image's
   `org.opencontainers.image.source` label links it on the first push; if it is not linked,
   connect it from the package settings.
4. Re-run the failed job: `gh run rerun --failed <run id>`.

Later releases push to the same package and stay public.

## Never re-tag a published version

Once `vX.Y.Z` is pushed, its image and GitHub Release are public, and operators pin to `X.Y.Z`.
Do not move or delete the tag, and do not re-push the image under the same version. If a release
is broken, fix it on `main` and release `X.Y.Z+1`, with a changelog entry saying what was wrong.

The one exception is a tag whose **Verify** job failed: nothing was published, so delete it
(`git push origin :refs/tags/vX.Y.Z` and `git tag -d vX.Y.Z`), fix the cause, and tag again.

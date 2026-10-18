# Spec Delta

## Purpose

Defines how a version of chainsniffer is cut, published as a container image to a public
registry, recorded in the changelog, and deployed next to a self-hosted Wanderer with Docker
Compose.

## ADDED Requirements

### Requirement: Tag-triggered release
Pushing a git tag of the form `vX.Y.Z` SHALL publish release `X.Y.Z`. Before publishing anything,
the release SHALL fail if the tag's version differs from the version the build stamps into the
binary and image, if the tagged commit fails the project's merge gate, or if `CHANGELOG.md` has no
entry for that version.

#### Scenario: Matching tag publishes
- **WHEN** `v0.2.0` is pushed, the build version is `0.2.0`, the gate passes and the changelog has a `0.2.0` entry
- **THEN** release `0.2.0` is published

#### Scenario: Version mismatch
- **WHEN** `v0.2.1` is pushed but the build version is still `0.2.0`
- **THEN** the release fails, naming both versions, and no image or GitHub Release is published

#### Scenario: Missing changelog entry
- **WHEN** a tag is pushed for a version with no `CHANGELOG.md` entry
- **THEN** the release fails and nothing is published

### Requirement: Published image
Each release SHALL publish the container image to `ghcr.io/vapor-lock/chainsniffer` as a single
multi-platform image for `linux/amd64` and `linux/arm64`, pullable without authentication.

#### Scenario: Pull on either architecture
- **WHEN** `docker pull ghcr.io/vapor-lock/chainsniffer:0.2.0` runs on an amd64 or an arm64 host
- **THEN** it pulls that host's image without logging in, and the image starts the relay

#### Scenario: Same contents as the local build
- **WHEN** the published image for a platform is inspected
- **THEN** it has the entrypoint, user, volume and environment of the image `nix build .#image` produces for the same version

### Requirement: Image tags
A release `X.Y.Z` SHALL be tagged `X.Y.Z`, `X.Y` and `latest`. A version with a pre-release suffix
(`X.Y.Z-rc.N`) SHALL be tagged only with its full version. Published version tags SHALL NOT be
reused for different contents.

#### Scenario: Stable release tags
- **WHEN** `0.2.1` is released after `0.2.0`
- **THEN** `0.2.1`, `0.2` and `latest` all point at the `0.2.1` image, and `0.2.0` still points at the `0.2.0` image

#### Scenario: Pre-release tags
- **WHEN** `v0.3.0-rc.1` is released
- **THEN** only `0.3.0-rc.1` is published, and `0.3` and `latest` are unchanged

### Requirement: Release notes
Each release SHALL create a GitHub Release for its tag, with that version's `CHANGELOG.md` entry
as the notes. Each entry SHALL list any change an operator must act on when upgrading, such as a
changed default or a new required permission, under an "Upgrading" heading.

#### Scenario: Notes from the changelog
- **WHEN** release `0.2.0` is published
- **THEN** the GitHub Release `v0.2.0` shows the `0.2.0` changelog entry, including its upgrade notes, and names the image reference to pull

### Requirement: Continuous checks
The project's merge gate SHALL run automatically on every pull request and every push to `main`,
and its result SHALL be reported on the pull request.

#### Scenario: Failing pull request
- **WHEN** a pull request breaks a test, vet, lint or the image build
- **THEN** the check on the pull request fails

### Requirement: Compose deployment snippet
The repository SHALL provide a Compose service definition that runs the published image of a
pinned release. It SHALL read both tokens as Compose secrets, take every other setting from an env
file the operator edits, and keep relay state on a named volume. It SHALL work when pasted into an
existing compose file and when pulled in with Compose `include:`.

#### Scenario: Included into a Wanderer compose project
- **WHEN** an operator copies `deploy/` next to their Wanderer `docker-compose.yml`, fills in the env file and both secret files, adds an `include:` of the snippet, and runs `docker compose up -d`
- **THEN** chainsniffer pulls its image, joins the Wanderer project's default network, connects to the map stream, and posts kills

#### Scenario: Tokens stay out of the configuration
- **WHEN** `docker inspect` is run on the chainsniffer container
- **THEN** neither token appears in its environment or labels

#### Scenario: State survives recreation
- **WHEN** the container is recreated for an upgrade
- **THEN** the relay resumes from its saved cursor and does not re-post kills it already posted

### Requirement: Hardened service defaults
The Compose snippet SHALL run the container with a read-only root filesystem, all Linux
capabilities dropped, privilege escalation disabled, a restart policy, a stop grace period longer
than the relay's shutdown deadline, and log rotation bounded in size.

#### Scenario: Locked-down container runs normally
- **WHEN** the snippet's service is started
- **THEN** the relay starts, writes its state to the volume and shuts down cleanly on `docker compose stop`, with nothing else writable

#### Scenario: Bounded logs
- **WHEN** the relay has logged for weeks
- **THEN** its log files on the host stay within the snippet's configured size limit

### Requirement: Internal Wanderer URL
The snippet's env file SHALL default `CHAINSNIFFER_WANDERER_URL` to Wanderer's service address on
the compose network, so the event stream does not pass through a public reverse proxy, and SHALL
document how to point it at a public URL.

#### Scenario: Default stream path
- **WHEN** the snippet runs in the same compose project as a Wanderer service under the documented service name and port
- **THEN** the relay reaches Wanderer by that address without leaving the Docker host

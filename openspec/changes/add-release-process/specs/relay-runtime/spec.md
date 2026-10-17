# Spec Delta

## MODIFIED Requirements

### Requirement: Container image
The project SHALL build an OCI container image of the relay that runs the `chainsniffer` binary as its
entrypoint as a non-root user, includes CA certificates for TLS, declares `/var/lib/chainsniffer` as
the state directory, and needs no other files at runtime. The image SHALL carry OCI labels naming
its source repository, version, source revision and license, and SHALL run with a read-only root
filesystem and no Linux capabilities, writing only to the state directory.

#### Scenario: Run the image
- **WHEN** the image is run with the required environment variables and a volume mounted at `/var/lib/chainsniffer`
- **THEN** the relay connects to Wanderer and Discord over TLS and persists its state to the volume

#### Scenario: Non-root
- **WHEN** the image's configured user is inspected
- **THEN** it is not root

#### Scenario: Version labels
- **WHEN** the image of version `0.2.0` is inspected
- **THEN** its `org.opencontainers.image.version` label is `0.2.0`, `org.opencontainers.image.source` is the project's repository URL, `org.opencontainers.image.revision` is the commit it was built from, and `org.opencontainers.image.licenses` is `MIT`

#### Scenario: Locked-down runtime
- **WHEN** the image is run with `--read-only --cap-drop ALL --security-opt no-new-privileges` and a volume at `/var/lib/chainsniffer`
- **THEN** the relay starts, saves its state and shuts down cleanly

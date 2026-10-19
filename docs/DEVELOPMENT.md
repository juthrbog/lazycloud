# Development

Local development setup, tools, and workflows.

## Prerequisites

- **Go 1.22+**
- **Docker** (for LocalStack)
- **[Task](https://taskfile.dev)** (task runner)
- **LocalStack auth token** — get one from [localstack.cloud](https://www.localstack.cloud/) and add it to `.env`:
  ```
  LOCALSTACK_AUTH_TOKEN=your_token_here
  ```

## Quick Start

```bash
cp .env.example .env             # add your LocalStack auth token
task localstack:up               # start LocalStack container
task localstack:seed             # seed with test data
task dev                         # run LazyCloud against LocalStack
```

## LocalStack

LazyCloud uses LocalStack to emulate AWS services locally. The container runs on port 4566 with persistence enabled — data survives restarts.

| Task | Description |
|------|-------------|
| `task localstack:up` | Start container, wait for health check |
| `task localstack:down` | Stop container |
| `task localstack:seed` | Seed with test data (see [Seeding](#seeding)) |
| `task localstack:wipe` | Delete all resources without stopping container |
| `task localstack:reset` | Stop container and delete all data (volumes) |
| `task localstack:logs` | Tail container logs |

## Seeding

The seed program (`cmd/seed/`) populates LocalStack with realistic test data. It creates resources across supported services with configurable volume.

### Usage

```bash
task localstack:seed                              # small tier (default)
task localstack:seed -- --size medium             # more resources for browsing
task localstack:seed -- --service s3              # seed only S3
task localstack:seed -- --size large --service ec2  # combine flags
task localstack:seed -- --wipe                      # wipe all state (no seeding)
task localstack:wipe && task localstack:seed         # fresh reseed
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--size` | `small` | Tier: `small`, `medium`, `large`, `enterprise` |
| `--service` | all | Comma-separated filter: `s3`, `ec2`, `sqs` |
| `--endpoint` | `http://localhost:4566` | LocalStack endpoint. The host must be `localhost`, a loopback address, `localstack`, or a `localhost.localstack.cloud` name, unless you pass `--allow-remote` |
| `--allow-remote` | `false` | Accept an endpoint host that is not local, for LocalStack on another machine |
| `--region` | `us-east-1` | AWS region |
| `--wipe` | `false` | Wipe all LocalStack state and exit (does not seed) |

### Tiers

Tiers control how many resources are created. Every tier includes the base named resources (specific buckets, AMIs, instances used for functional testing) plus additional generated resources.

| Tier | Extra resources | Use case |
|------|----------------|----------|
| `small` | None — base only | Quick dev iteration |
| `medium` | +10 buckets, +15 instances, +5 SGs | Realistic browsing and filtering |
| `large` | +50 buckets, +200 instances, +20 SGs | Pagination and progressive loading |
| `enterprise` | +200 buckets, +1000 instances, +50 SGs | Performance stress testing |

Exact counts are defined in `cmd/seed/tiers.toml`.

### How It Works

- **Idempotent**: safe to run multiple times. Lists existing resources first, only creates what's missing. PutObject overwrites are harmless. The SQS seed messages go only to a queue that holds no messages, so a second run does not double the counts. After a purge, a run sends them again.
- **LocalStack only**: the seed program creates resources such as security groups that are open to the internet. Before it does anything, it checks the endpoint host (see `--endpoint`) and asks `/_localstack/health` for a LocalStack health report. An empty endpoint, a real AWS endpoint, or a server that is not LocalStack stops it with an error.
- **Parallel** — services seed concurrently (S3 and EC2 run in separate goroutines).
- **Base + extras** — named resources (from `tiers.toml` `[base]` section) are always created. Tiers add generated resources on top (`data-bucket-001`, `worker-003`, `gen-sg-001`, etc.).
- **Object content** — base bucket objects have realistic content (JSON configs, YAML, scripts, logs). Extra bucket objects are simple generated text files.

### Adding a New Service Seeder

1. Create `cmd/seed/<service>.go` implementing the `Seeder` interface:
   ```go
   type Seeder interface {
       Name() string                        // e.g., "dynamodb"
       Seed(ctx context.Context) error      // idempotent
   }
   ```
2. Add base resources and tier extras to `cmd/seed/tiers.toml`
3. Add config structs to `cmd/seed/config.go`
4. Register the seeder in `buildSeeders()` in `cmd/seed/main.go`

## Taskfile Reference

### Development

| Task | Description |
|------|-------------|
| `task dev` | Run against LocalStack (starts container if needed) |
| `task dev:watch` | Rebuild and rerun on file changes |
| `task run` | Run against real AWS (default profile) |
| `task build` | Build the `lazycloud` binary |

### Testing

| Task | Description |
|------|-------------|
| `task test` | Run unit tests |
| `task test:race` | Run unit tests like CI: with the race detector, in random order |
| `task cover` | Run unit tests and show the coverage per function |
| `task test:integration` | Run integration tests against LocalStack |
| `task lint` | Run golangci-lint. It needs the version that CI uses (`GOLANGCI_LINT_VERSION` in `Taskfile.yml`) |
| `task vuln` | Scan for known vulnerabilities with govulncheck. `go.mod` pins its version as a tool |
| `task fmt` | Format code (gofmt + goimports) |

`.golangci.yml` lists the linters, with a short note on what each one finds. modernize and testifylint can fix most of their issues: run `golangci-lint run --fix`, then read the diff. A `//nolint` comment must name the linter and give a reason, for example `//nolint:gosec // seed for visual animation, not crypto`. nolintlint reports a `//nolint` comment that has no reason or that no longer hides an issue.

### Release

| Task | Description |
|------|-------------|
| `task snapshot` | Build cross-platform binaries locally |
| `task release -- v0.1.0` | Tag and push a release |
| `task demo` | Record `demo/demo.gif` with VHS in Docker. LocalStack must run with the seed data |

## Releasing

A tag `v*` starts `.github/workflows/release.yml`. The workflow runs vet, the race tests, and govulncheck. Then GoReleaser publishes these files in a GitHub release:

- An archive for Linux, macOS, and Windows, each for amd64 and arm64. The Windows archives are zip files.
- An SBOM (a list of the Go modules in the binary) for each archive, in SPDX JSON. syft writes them.
- `checksums.txt` and `checksums.txt.sigstore.json`. cosign signs `checksums.txt` without a key: the signature holds the identity of the release workflow.

The workflow also stores a provenance attestation for each file in `checksums.txt`, and it updates the Homebrew formula. GitHub writes the release notes from the merged pull requests. `.github/release.yml` puts Dependabot updates in a section of their own. Edit the notes after the release if they need more detail.

The same commit gives the same archives. GoReleaser builds with `-trimpath`, and the file times and the build date come from the commit.

A pull request that changes the release setup runs a snapshot. The snapshot builds all archives and checks the Homebrew formula. Then a job on Linux (amd64 and arm64), macOS, and Windows runs `lazycloud --version`. The snapshot publishes nothing.

### Homebrew

The release workflow writes `Formula/lazycloud.rb` with `scripts/formula.sh` and pushes it to [juthrbog/homebrew-tap](https://github.com/juthrbog/homebrew-tap). A prerelease tag, for example `v0.6.0-rc.1`, does not change the formula.

The push uses a deploy key of the tap. Set it up once:

1. Make an SSH key pair that has no other use:
   `ssh-keygen -t ed25519 -N "" -C "lazycloud release publisher" -f lazycloud-tap`
2. Add the public key to the tap, with write access:
   `gh repo deploy-key add lazycloud-tap.pub --repo juthrbog/homebrew-tap --allow-write --title "lazycloud release publisher"`
3. Store the private key as an Actions secret of this repository:
   `gh secret set HOMEBREW_TAP_SSH_KEY --repo juthrbog/lazycloud < lazycloud-tap`
4. Delete `lazycloud-tap` and `lazycloud-tap.pub`.

Without the secret, the release still publishes, but the workflow shows a warning and does not change the formula.

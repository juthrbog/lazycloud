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
| `--service` | all | Comma-separated filter: `s3`, `ec2` |
| `--endpoint` | `http://localhost:4566` | LocalStack endpoint |
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

- **Idempotent** — safe to run multiple times. Lists existing resources first, only creates what's missing. PutObject overwrites are harmless.
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
| `task test:integration` | Run integration tests against LocalStack |
| `task lint` | Run linter |
| `task fmt` | Format code (gofmt + goimports) |

### Release

| Task | Description |
|------|-------------|
| `task snapshot` | Build cross-platform binaries locally |
| `task release -- v0.1.0` | Tag and push a release |

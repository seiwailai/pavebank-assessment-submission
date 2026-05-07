# Fees API

## Overview

Fees API is a billing workflow system built with:

- Go
- Encore
- Temporal
- PostgreSQL
- Docker

The system supports:

- bill creation
- batch line-item ingestion
- bill lifecycle orchestration
- active bill snapshot tracking
- manual and automatic bill closure
- final bill computation

For detailed architecture decisions, workflow lifecycle semantics, API contracts, data models, and system guarantees, please refer to:
[Engg Spec](./fees-api-pavebank-spec.md)

---

# Prerequisites

This project supports:

- macOS
- Linux
- Windows

Please install the following dependencies before running the project.

| Dependency | Installation |
|---|---|
| Go | https://go.dev/doc/install |
| Encore | https://encore.dev/docs/install |
| Temporal CLI | https://docs.temporal.io/cli |
| Docker | https://docs.docker.com/get-docker/ |

---

# Running the Project

## Step 1 — Start Docker

Make sure Docker Desktop or Docker daemon is running.

Verify Docker is running:

```bash
docker ps
```

Encore will automatically boot the required PostgreSQL container during local development.

---

## Step 2 — Start Temporal Development Server

Run:

```bash
temporal server start-dev
```

Default Temporal UI:

```text
http://localhost:8233
```

---

## Step 3 — Start Encore Application

Run:

```bash
encore run
```

Encore will automatically start:

- API services
- PostgreSQL
- local infrastructure
- Temporal-connected workers

---

# Testing

## Unit Tests & Integration Tests

Run all tests:

```bash
encore test -v ./...
```

---

# UAT (User Acceptance Testing)

UAT includes:

- endpoint behavior tests
- bill workflow lifecycle tests

For detailed UAT scenarios and API walkthroughs, refer to:
[UAT Testing](./fees/uat/README.md)

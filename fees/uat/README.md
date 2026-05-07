# Fees UAT

## What This UAT Covers
This UAT suite verifies two major areas of the `fees` service:

- endpoint behavior and API contracts (`/v1/bills`, `/line-items`, `/close`, query endpoints)
- lifecycle behavior (scheduled/open/finalizing/closed flows and polling-based close completion)

It includes automated BDD tests (`godog` + `go test`), plus manual reproducibility artifacts (`curl`, scenario matrix).

## Prerequisites
Install these first:

- Go: https://go.dev/doc/install
- Encore CLI: https://encore.dev/docs/install
- Docker: https://docs.docker.com/get-docker/
- curl: https://curl.se/download.html

Runtime prerequisites:

- Docker daemon running (Encore local DB + managed Temporal container mode)
- Managed runtime mode: you do not need to start `encore run` or Temporal manually
- Reuse-existing mode: you must start `encore run` and a Temporal server manually

Optional:

- Temporal CLI: https://docs.temporal.io/cli
- Postman: https://www.postman.com/downloads/

Quick checks:

```bash
go version
encore version
docker version
curl --version
```

```powershell
go version
encore version
docker version
curl.exe --version
```

## Run All UAT Cases
From repository root:

```bash
go test -v ./fees/uat -run TestUAT -count=1 -args -uat-manage-runtime
```

This starts isolated runtime resources automatically and cleans them up after the run.
It creates a dedicated Encore namespace, starts dedicated PostgreSQL and Temporal containers on a private Docker network, and boots the API on a dedicated port.

## Run One Scenario (Specific Case)
Example by tag:

```bash
go test -v ./fees/uat -run TestUAT -count=1 -args -uat-manage-runtime -uat-tags='@EP_CREATE_009'
```

PowerShell:

```powershell
go test -v ./fees/uat -run TestUAT -count=1 -args "-uat-manage-runtime" "-uat-tags=@EP_CREATE_009"
```

## Reuse Existing Runtime
If you already started the API and Temporal yourself:

```bash
go test -v ./fees/uat -run TestUAT -count=1 -args -uat-base-url='http://127.0.0.1:4000'
```

Manual startup for this mode:

```bash
encore run
temporal server start-dev
```

## Convenience Runners
The scripts run from within the `fees` service and keep UAT paths local to that service.

Bash:

```bash
./fees/uat/scripts/run-uat.sh
./fees/uat/scripts/run-uat.sh '@endpoint'
./fees/uat/scripts/run-uat.sh '@EP_CREATE_009'
./fees/uat/scripts/run-uat.sh '@endpoint' 'features/endpoints/create_bill.feature' 1
```

PowerShell:

```powershell
./fees/uat/scripts/run-uat.ps1
./fees/uat/scripts/run-uat.ps1 -Tags '@endpoint'
./fees/uat/scripts/run-uat.ps1 -Tags '@EP_CREATE_009'
./fees/uat/scripts/run-uat.ps1 -Tags '@endpoint' -Features 'features/endpoints/create_bill.feature' -ReuseExisting
```

Notes:

- Third arg `1` in `run-uat.sh` means reuse existing runtime.
- Default feature root in runners is `features` (relative to `fees/uat`).

## How This Relates To `encore test ./... -v`
- UAT is opt-in for broad test runs.
- Running `encore test ./... -v` now runs unit/integration tests, and `TestUAT` is skipped unless explicitly requested.
- To run UAT, request it explicitly, for example:

```bash
go test -v ./fees/uat -run TestUAT -count=1 -args -uat-manage-runtime
```

## Manual Artifacts
- `fees/uat/curl/README.md`
- `fees/uat/scenario-matrix.md`

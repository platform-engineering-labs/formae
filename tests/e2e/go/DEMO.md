# First-class secrets demo: AWS Secrets Manager → Grafana ContactPoint

This demo shows formae resolving a secret value live at the plugin boundary.
A forma declares an AWS Secrets Manager secret and a Grafana ContactPoint whose
`settingsMap` wires the secret's value via `theSecret.res.secretString`. On
apply, formae creates the secret, reads the value from AWS, and passes it to
the Grafana plugin — the credential is never written to the formae datastore.

## What it proves

- A `$res` resolvable can cross plugin namespaces (AWS → Grafana).
- Secret resolution happens at the plugin boundary, not in the agent's datastore.
- The inventory stores a `$ref` envelope; the plaintext never persists.
- A single `formae apply` creates both resources in dependency order.

## Prerequisites

| Requirement | Detail |
|---|---|
| AWS credentials | Real AWS creds with Secrets Manager write access |
| Grafana (local) | Running at http://localhost:3333 (admin:admin) |
| formae binary | Built from source (`make build`) |
| AWS + Grafana plugins | Installed in `~/.pel/formae/plugins/` |
| PKL dependencies | Resolved via `setup_pkl.sh` |

## Required environment variables

```sh
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...   # if using temporary credentials
export AWS_REGION=us-east-1   # or your preferred region
```

The Grafana plugin reads auth from `GRAFANA_AUTH` (set by the e2e harness as
`admin:admin`). No additional env var needed for local Grafana.

## Step 1: Start local Grafana

```sh
cd /home/jeroen/dev/pel/formae-plugin-grafana
make test-env-up
# or: docker compose -f docker-compose.test.yml up -d --wait

# Verify:
curl -s http://localhost:3333/api/health
# Expected: {"database":"ok","version":"..."}
```

To stop Grafana after the demo:
```sh
cd /home/jeroen/dev/pel/formae-plugin-grafana
make test-env-down
# or: docker compose -f docker-compose.test.yml down -v
```

## Step 2: Build formae

```sh
cd /home/jeroen/dev/pel/formae/.worktrees/secrets-grafana-demo
make build
```

## Step 3: Install plugins (if not already installed)

```sh
# Start a local agent and install plugins:
./formae agent start &
./formae plugin install aws
./formae plugin install grafana
kill %1
```

## Step 4: Set up PKL dependencies

```sh
cd /home/jeroen/dev/pel/formae/.worktrees/secrets-grafana-demo/tests/e2e/go
bash setup_pkl.sh
```

## Step 5: Run the demo test

```sh
cd /home/jeroen/dev/pel/formae/.worktrees/secrets-grafana-demo
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1

go test -v -tags=e2e -run TestSecretsGrafanaDemo ./tests/e2e/go/ -timeout 10m
```

### Exact morning run command

```sh
cd /home/jeroen/dev/pel/formae/.worktrees/secrets-grafana-demo
AWS_ACCESS_KEY_ID=<KEY> AWS_SECRET_ACCESS_KEY=<SECRET> AWS_REGION=us-east-1 \
  go test -v -tags=e2e -run TestSecretsGrafanaDemo ./tests/e2e/go/ -timeout 10m
```

## What to observe

1. The test logs show `apply submitted, CommandId: ...` — formae queues the apply.
2. The Secret resource is created in AWS Secrets Manager first.
3. The ContactPoint is created in Grafana with the credential resolved from AWS.
4. The test verifies the ContactPoint exists via Grafana's provisioning API.
5. The Secret's `secretString` is absent from inventory — it's `writeOnly` and
   never returned by AWS read-back, proving the credential stays in AWS only.
6. Destroy removes both resources cleanly.

## Fixture location

`tests/e2e/go/fixtures/secrets_grafana_demo.pkl`

## Key PKL snippet (the cross-plugin resolvable)

```pkl
new cpmod.ContactPoint {
  label = "e2e-grafana-demo-contactpoint"
  target = grafanaTarget.res
  name = "formae-e2e-demo-webhook"
  contactPointType = "webhook"
  settingsMap = new Mapping {
    ["url"] = "https://hooks.example.com/webhook"
    ["authorization_credentials"] = theSecret.res.secretString  // <- resolvable
  }
}
```

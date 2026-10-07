<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/user-attachments/assets/f5b7267c-4fff-4aab-b33c-6b17a658c88e">
    <source media="(prefers-color-scheme: light)" srcset="https://github.com/user-attachments/assets/e6a09bee-8fd9-4d29-a405-a1cd743209bb">
    <img width="400" alt="formae" src="https://github.com/user-attachments/assets/e6a09bee-8fd9-4d29-a405-a1cd743209bb">
  </picture>
</h1>

<p align="center">
  <a href="https://github.com/platform-engineering-labs/formae/actions/workflows/go.yml"><img src="https://github.com/platform-engineering-labs/formae/actions/workflows/go.yml/badge.svg" alt="formae"></a>
  <a href="https://docs.formae.ai"><img src="https://img.shields.io/badge/docs-formae.ai-blue" alt="Documentation"></a>
  <a href="https://discord.gg/hr6dHaW76k"><img src="https://img.shields.io/discord/1417222307956392148?logo=discord&logoColor=959da5" alt="Discord"></a>
  <a href="https://github.com/platform-engineering-labs/formae/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-FSL--1.1--ALv2-blue" alt="License: FSL-1.1-ALv2"></a>
</p>

formae discovers the infrastructure running in your cloud accounts and clusters, keeps a versioned record of every resource and every change to it, whoever made the change, and applies changes at any granularity, from a single property to a whole environment. There is no state file to manage. You work with it through code, the CLI or an AI agent.

It can be used as an alternative to Terraform, OpenTofu or Pulumi, or alongside them, Helm and manual changes, on AWS, Azure, Google Cloud, Kubernetes and [other platforms](#plugins).

This repository is **formae IaC**, the open source edition you run yourself. [formae Cloud](https://formae.ai/formae-cloud) is the same formae with the agent run and managed for you.

[Documentation](https://docs.formae.ai) · [Quick start](https://docs.formae.ai/documentation/get-started/quickstart) · [Plugins](https://hub.platform.engineering) · [Release notes](https://docs.formae.ai/documentation/reference/release-notes) · [Website](https://formae.ai) · [Discord](https://discord.gg/hr6dHaW76k)

## Install

```bash
/bin/bash -c "$(curl -fsSL https://hub.platform.engineering/get/formae.sh)"
export PATH=/opt/pel/bin:$PATH
formae --version
```

Linux and macOS. A container image and a [Helm chart](https://github.com/platform-engineering-labs/formae-helm) are available for running the agent on a server or in Kubernetes.

## Example

Start the agent, then see what already exists and get code for it:

```bash
formae agent start                                            # in its own terminal
formae inventory resources --query="managed:false"            # resources formae found but does not manage
formae extract --query 'type:AWS::S3::Bucket' ./buckets.pkl   # code for them, generated from what is running
```

Or declare resources yourself. A forma in Pkl, `main.pkl`:

```pkl
amends "@formae/forma.pkl"
import "@formae/formae.pkl"
import "@aws/aws.pkl"
import "@aws/s3/bucket.pkl"

forma {
  new formae.Stack {
    label = "my-app"
  }

  new formae.Target {
    label = "my-aws-target"
    config = new aws.Config {
      region = "us-east-1"
    }
  }

  new bucket.Bucket {
    label = "app-bucket"
    bucketName = "my-unique-bucket-name"
  }
}
```

```bash
formae apply --mode reconcile main.pkl   # shows the plan, asks before changing anything
```

The [quick start](https://docs.formae.ai/documentation/get-started/quickstart) deploys a full example to AWS, Azure or GCP in about ten minutes.

## How it works

- **Discovery.** The agent scans each target (a cloud account, region or cluster) every 10 minutes by default and records resources it does not manage as *unmanaged*. Unmanaged resources are read-only to formae until you bring them under management.
- **Synchronization.** Every 5 minutes by default, the agent reads the resources it knows about and records any change made outside formae, which other tools usually call drift. It does not edit your code repository or change cloud resources on its own.
- **Extract.** `formae extract` writes Pkl for any set of resources, managed or not, so code can be generated from the running infrastructure instead of written by hand.
- **Code.** Infrastructure As Code (IaC): desired state is declared in a forma, a file written in [Pkl](https://pkl-lang.org), a configuration language with types and constraints. Resource types are Pkl schemas provided by the plugins, so a forma is type-checked before anything is applied.
- **Reconcile.** `formae apply --mode reconcile` makes a stack match the forma in your code, on disk or in your repository, including deletions. If out-of-band changes (drift) were recorded, it stops and asks you to *absorb* each one into the desired state or *revert* it. Works with GitOps: keep the Pkl in Git and run reconcile from CI.
- **Patch.** `formae apply --mode patch` only creates or updates the resources named in the forma you apply and never deletes anything; collections such as tags are only added to. It is meant for targeted and emergency changes.
- **Agent.** The CLI talks to an agent that executes operations and stores state in its own database (SQLite, PostgreSQL, Aurora Data API or SQL Server). Run it yourself locally, on a server or in Kubernetes, or use [formae Cloud](https://formae.ai/formae-cloud), where it is run for you.
- **Plugins.** Comparable to Terraform providers: each plugin is a separate, independently versioned process that adds the resource types of one platform.

Concepts in detail: [forma](https://docs.formae.ai/documentation/concepts/forma), [stack](https://docs.formae.ai/documentation/concepts/stack), [target](https://docs.formae.ai/documentation/concepts/target), [apply modes](https://docs.formae.ai/documentation/concepts/apply-modes), [discovery](https://docs.formae.ai/documentation/concepts/discovery), [synchronization](https://docs.formae.ai/documentation/concepts/synchronization), [architecture](https://docs.formae.ai/documentation/concepts/architecture).

## Compared with Terraform and Pulumi

| | Terraform / OpenTofu | Pulumi | formae |
|---|---|---|---|
| IaC language | HCL | TypeScript, JavaScript, Python, Go, C#, Java, YAML and more | Pkl |
| State | State file in a local or remote backend | State in Pulumi Cloud or a self-managed backend | Stored by the agent; no state file to handle |
| Runtime | CLI, runs on demand | CLI, runs on demand | CLI plus an always-on agent |
| Changes made outside the tool ("drift") | Detected when a plan refreshes state | Detected by `pulumi refresh` | Recorded by the agent, every 5 minutes by default |
| Existing resources | `import` blocks or `terraform import` | `pulumi import` | Discovered automatically; `formae extract` generates the code |
| Targeted changes | `-target`; the whole configuration and dependency graph are still evaluated | `--target`; the whole program still runs and builds the full resource graph | `--mode patch` applies only what the forma declares, with minimal blast radius |

Longer write-ups: [formae vs. Terraform](https://formae.ai/formae-vs-terraform), [formae vs. Pulumi](https://formae.ai/formae-vs-pulumi).

## Plugins

Official plugins, published on the [formae hub](https://hub.platform.engineering):

- **Cloud:** [AWS](https://github.com/platform-engineering-labs/formae-plugin-aws), [Azure](https://github.com/platform-engineering-labs/formae-plugin-azure), [Google Cloud](https://github.com/platform-engineering-labs/formae-plugin-gcp), [Oracle Cloud (OCI)](https://github.com/platform-engineering-labs/formae-plugin-oci), [OVHcloud](https://github.com/platform-engineering-labs/formae-plugin-ovh), [Fly.io](https://github.com/platform-engineering-labs/formae-plugin-fly), [Vercel](https://github.com/platform-engineering-labs/formae-plugin-vercel)
- **Containers:** [Kubernetes](https://github.com/platform-engineering-labs/formae-plugin-kubernetes), [Docker Compose](https://github.com/platform-engineering-labs/formae-plugin-compose)
- **Data:** [Databricks](https://github.com/platform-engineering-labs/formae-plugin-databricks), [Supabase](https://github.com/platform-engineering-labs/formae-plugin-supabase)
- **Observability:** [Datadog](https://github.com/platform-engineering-labs/formae-plugin-datadog), [Grafana](https://github.com/platform-engineering-labs/formae-plugin-grafana), [PagerDuty](https://github.com/platform-engineering-labs/formae-plugin-pagerduty)
- **CI/CD:** [GitHub Actions](https://github.com/platform-engineering-labs/formae-plugin-gha), [GitLab CI/CD](https://github.com/platform-engineering-labs/formae-plugin-gitlab)
- **AI:** [vLLM](https://github.com/platform-engineering-labs/formae-plugin-vllm)
- **Other:** [SFTP](https://github.com/platform-engineering-labs/formae-plugin-sftp)

To write a plugin for anything with an API, start from the [plugin template](https://github.com/platform-engineering-labs/formae-plugin-template) and the [plugin SDK guide](https://docs.formae.ai/plugin-development/index).

## AI agents

formae has an MCP server, so AI coding assistants can query and change infrastructure through the same agent and schemas as the CLI.

**Claude Code only**, run inside Claude Code:

```
/plugin marketplace add platform-engineering-labs/formae-marketplace
/plugin install formae@formae-marketplace
```

**Codex, Cursor, OpenCode and other MCP clients:** see [AI assistants](https://docs.formae.ai/documentation/guides/ai-coding-assistants) for each client's setup.

Source: [formae-mcp](https://github.com/platform-engineering-labs/formae-mcp). The documentation is also available as [llms.txt](https://docs.formae.ai/llms.txt).

## Editions

- **formae IaC** (this repository): open source and self-hosted. You run the agent.
- **[formae Cloud](https://formae.ai/formae-cloud)**: formae with the agent run and managed for you. You work with it through your AI coding assistant; there is no agent to operate. [Quick start with formae Cloud](https://docs.formae.ai/documentation/get-started/quickstart-cloud).

See [pricing](https://formae.ai/pricing).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Questions and discussion: [Discord](https://discord.gg/hr6dHaW76k).

## Security

Report vulnerabilities to [security@platform.engineering](mailto:security@platform.engineering).

## License

formae is open source under [FSL-1.1-ALv2](LICENSE): free to use, modify and self-host, and every release converts to Apache-2.0 after two years.

Built by [Platform Engineering Labs](https://platform.engineering).

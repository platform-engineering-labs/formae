# Contributing to formae

Welcome, and thank you for considering to contribute to formae!

## Requirements

Before getting started, make sure you have the following tools installed:
* [Go 1.25.1](https://go.dev/doc/install) or higher
* [Pkl 0.29.1](https://pkl-lang.org/main/current/pkl-cli/index.html#installation) or later
* [pkl-gen-go](https://pkl-lang.org/go/current/quickstart.html#1-install-dependencies)
* [Golangci-lint](https://golangci-lint.run/docs/welcome/install/#local-installation)
* [REUSE - License Compliance](https://github.com/fsfe/reuse-tool)
* [swag cli](https://github.com/swaggo/swag?tab=readme-ov-file#getting-started)

For the AWS plugin you need a valid AWS credentials file (`~/.aws/credentials`). You can create one by installing the [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) and running `aws configure`.

## Getting started

Fork the repository and create your branch from `main`.

```sh
# Build formae in release mode
make build

# Build fomae in debug mode
make build-debug

# Run the tests
make test-all

# Run the agent
go run cmd/formae/main.go agent start

# Run the client
go run cmd/formae/main.go
```

### Logs

By default, logs will be in `~/.pel/formae/log/formae.log`. Console logs INFO by default and file logs DEBUG. You can change this behavior by editing the [configuration](https://docs.formae.io/en/latest/configuration/).

## Contributor License Agreement (CLA)

In order to make code contributions, a signed CLA is required for individuals and corporate contributors.

### Individuals

Before we can accept your contribution, you'll need to sign our Individual Contributor License Agreement (CLA).

- Open a PR, and CLA Assistant will comment with a link
- Click the link and sign the CLA with your GitHub account
- You won't be asked to sign again for future contributions

We might still require additional verification of your identity. In this case we would reach out to you through
email, or comment on your PR.

### Companies

When you represent a company, a legitimate representative of your company needs to sign a CLA. This process is not
automated. You can either add a comment to your PR, or reach out to us through legal[at]platform.engineering directly.
We require following information before we can move forward with the contribution:

- Legal company name
- Name of the signing person
- Title of the signing person
- Email of the signing person

We will email this person our Corporate CLA for electronic signature. Once the document is signed, we will be able
to further address the contribution.

## Report a bug

Use the the bug report template (under [Issues](https://github.com/platform-engineering-labs/formae/issues)) for any reproducible issues. (This is the only issue type available via the template chooser.)

## Request a feature

If there is a new feature you'd like to suggest, start a new [Discussion](https://github.com/platform-engineering-labs/formae/discussions) instead. Our maintainers will review promising suggestions and, if they fit our roadmap, convert them into tracked Issues. The maintainer will loop you in on the Issue when this happens.

### Why can I not create an Issue?

Not every idea becomes an Issue right away—we discuss first to ensure it delivers clear user value and avoids random refactorings or low-priority changes. As a small team, we review code carefully but limitedly. Routing discussions upfront helps us focus reviews on vetted, high-impact work. By aligning early in Discussions, we make the most of your effort and our bandwidth, ensuring reviews are efficient and productive for everyone.

## Coding guidelines

* Imitate the conventions of the surrounding code
* Make sure to follow the official [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
* Run `make lint` to verify that your code passes the linter

## License compliance

This project uses the [REUSE](https://reuse.software/) standard for license management. Compliance is enforced by CI.

### Adding headers to new files:

> Note: This command is idempotent.

```bash
make add-license
```

### Checking compliance:
Verify compliance:
```bash
make lint-reuse
```

### Files will have a header as shown:
```go
// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2
```

For more information about the licenses used in this project, see:
- [FSL-1.1-ALv2 License](LICENSES/FSL-1.1-ALv2.md) - [Official Template](https://github.com/getsentry/fsl.software/blob/main/FSL-1.1-ALv2.template.md)
- [Apache-2.0 License](LICENSES/Apache-2.0.md) - [Official Source](https://www.apache.org/licenses/LICENSE-2.0)

## Signed commits

Commits must be signed. Ensure commit verification with SSH key signing. Upload your SSH key as a 'Signing Key' in GitHub Settings -> SSH and GPG keys. Then enable signed commits with these commands:

```bash
git config gpg.format ssh
git config user.signingkey ~/.ssh/id_ed25519
git config commit.gpgSign true
```

## Submit a PR

Every PR must reference and close an open issue. This keeps changes targeted and traceable. PRs without an issue will be closed without review. PRs must include test coverage.

## For maintainers

When merging PRs use the squash merge option. The commit message for the merge commit should follow [Conventional Commits guidelines](https://www.conventionalcommits.org/en/v1.0.0/#summary).

## Communication

[GitHub discussions](https://github.com/platform-engineering-labs/formae/discussions) for feature requests

[GitHub issues](https://github.com/platform-engineering-labs/formae/isues) for bug reports

[Discord](https://discord.gg/hr6dHaW76k) for quick questions, live help, or casual conversations

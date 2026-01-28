<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/user-attachments/assets/f5b7267c-4fff-4aab-b33c-6b17a658c88e">
    <source media="(prefers-color-scheme: light)" srcset="https://github.com/user-attachments/assets/e6a09bee-8fd9-4d29-a405-a1cd743209bb">
    <img width="400" alt="formae" src="https://github.com/user-attachments/assets/e6a09bee-8fd9-4d29-a405-a1cd743209bb">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/platform-engineering-labs/formae/actions/workflows/go.yml"><img src="https://github.com/platform-engineering-labs/formae/actions/workflows/go.yml/badge.svg" alt="formae"></a>
  <a href="https://docs.formae.io"><img src="https://img.shields.io/badge/docs-formae.io-blue" alt="Documentation"></a>
  <a href="https://discord.gg/hr6dHaW76k"><img src="https://img.shields.io/discord/1417222307956392148?logo=discord&logoColor=959da5" alt="Discord"></a>
  <a href="https://github.com/platform-engineering-labs/formae/blob/main/LICENSES/FSL-1.1-ALv2.md"><img src="https://img.shields.io/badge/license-FSL--1.1--ALv2-blue" alt="License: FSL-1.1-ALv2"></a>
</p>

## What is **formae**?

**formae** is a 100% code-based, agentic IaC (Infrastructure-as-Code) tool built from scratch for the modern age. We believe that code is the only medium every engineer on every level understands and wants. So **formae** implements infrastructure entirely as code - in and out, and in any granularity. **formae** doesn't require its user to maintain any secondary artifacts such as state files, and keeps the infrastructure code automatically in sync with the reality.

## **formae** capabilities

- **The single source of truth is code:** It unifies every infrastructure resource and change into fully versioned infrastructure code.
- **Always up-to-data infrastructure code:** It sees when things change outside the tool. It merges these changes into your infrastructure code instead of just ignoring or undoing them. This way, important outside changes are never lost, and are immediately incorporated into the infrastructure code.
- **Prevents avoidable mistakes:** It is built around a very robust, enforced schema.
- **Made for everyone:** It welcomes all kinds of engineers, whether they are new or experienced, be they in Ops, DevOps, SRE or Platform Engineering.
- **Perfect for Platform Engineering:** It allows platform engineers to work on the low level of detail, and developers consume reusable services by just providing a few predefined, schema-safe properties.
- **Built for co-existence:** It is not necessary to migrate or to import anything - **formae** will automatically discover and update resources and happily co-exist with other IaC and Infrastructure Management tools and even ClickOps.
- **Perfect for Day 0 and onward:** It is equally great for setting up new systems and for making small, safe changes with minimal blast radius as you go.

## How to Use **formae** in Your Organization?

Using **formae** is straightforward and designed to fit various team roles, needs and daily operation scenarios and situations:

- **Core Platform Engineers:** A small group of engineers with broad responsibilities can manage the main infrastructure code. They can make large, system-wide changes. They often use GitOps (managing infrastructure through Git), but **formae** doesn't enforce this.

- **Engineers with Specific Roles & Developers:** Engineers who are newer, or those who focus on smaller parts of the system, can make small changes. Developers needing to adjust specific resources can also apply these small patches. This keeps the risk of impact (blast radius) very small.

- **On-Call Engineers (Emergency Fixes):** An engineer fixing an urgent issue, even at night, works like those making small changes. No matter their experience, they focus on fixing the problem with minimal risk to other parts of the system. This helps them solve issues quickly and safely.

- **Specialized Teams (Security, Cost Optimization):** Teams that work across different areas, like security or cost-saving groups, can also apply changes as patches. They might not change the core infrastructure, but their changes can affect many parts of the system. **formae** handles these wider-reaching but targeted changes easily.

- **Working with Other Tools:** In many companies, people use different tools to make changes directly in cloud accounts. These could be special security tools or other IaC tools like Terraform, or even ClickOps. **formae** works well alongside them. It detects these external changes and merges them, giving you a consistent, version-controlled and always up-to-date view of your entire infrastructure - **entirely as code**.

## Learning **formae**

The best way to start with **formae** is by following the **formae** 101 in the [documentation](https://docs.formae.io/en/latest/formae-101/fundamentals).

If you are new to Pkl, try the [Pkl primer](https://pkl.platform.engineering/), our own hands-on tour of the language!

If you don't feel like reading you can check out a series of video walkthroughs on our [YouTube channel](https://www.youtube.com/playlist?list=PLntTBHUL8qpTGIIYkxOv8cLp7Y5jtZpun).

## Installation

Follow the [Quick start](https://docs.formae.io/en/latest/) for instructions on how to install **formae**.

## Contributing

If you are interested in contributing to **formae**, please read our [contributing guidelines](https://github.com/platform-engineering-labs/formae/blob/main/CONTRIBUTING.md) before submitting a pull request.

## Security vulnerabilities

If you discover a security vulnerability in **formae**, please send an email to [security@platform.engineering](mailto:security@platform.engineering). Security vulnerabilities will be promptly addressed.

## License

Formae is open-sourced software licensed under the [FSL-1.1-ALv2](https://github.com/platform-engineering-labs/formae/blob/main/LICENSES/FSL-1.1-ALv2.md) license.

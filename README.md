<h1 align="center">
  <p align="center">Koordinator</p>
  <a href="https://koordinator.sh"><img src="https://github.com/koordinator-sh/koordinator/raw/main/docs/images/koordinator-logo.jpeg" alt="Koordinator"></a>
</h1>

[![License](https://img.shields.io/badge/License-Apache_2.0-4EB1BA.svg)](https://opensource.org/licenses/Apache-2.0)
[![codecov](https://codecov.io/github/koordinator-sh/koordinator/branch/main/graph/badge.svg?token=CPUKM09WJL)](https://codecov.io/github/koordinator-sh/koordinator)
[![PRs Welcome](https://badgen.net/badge/PRs/Welcome/green?icon=https://api.iconify.design/octicon:git-pull-request.svg?color=white)](CONTRIBUTING.md)
[![Slack](https://badgen.net/badge/Slack/Join/4A154B?icon=slack)](https://join.slack.com/t/koordinator-sh/shared_invite/zt-1756qoub4-Cn4~esfdlfAPsD7cwO2NzA)

## Introduction

Koordinator is a QoS based scheduling system for hybrid orchestration workloads on Kubernetes. It aims to improve the
runtime efficiency and reliability of both latency sensitive workloads and batch jobs, simplify the complexity of
resource-related configuration tuning, and increase pod deployment density to improve resource utilizations.

Koordinator enhances the kubernetes user experiences in the workload management by providing the following:

- Well-designed priority and QoS mechanism to co-locate different types of workloads in a cluster, a node.
- Allowing for resource overcommitments to achieve high resource utilizations but still satisfying the QoS guarantees by
  leveraging an application profiling mechanism.
- Fine-grained resource orchestration and isolation mechanism to improve the efficiency of latency-sensitive workloads
  and batch jobs.
- Flexible job scheduling mechanism to support workloads in specific areas, e.g., big data, AI, audio and video.
- A set of tools for monitoring, troubleshooting and operations.

## Quick Start

You can view the full documentation from the [Koordinator website](https://koordinator.sh/docs).

- Install or upgrade Koordinator with [the latest version](https://koordinator.sh/docs/installation).
- Referring to [best practices](https://koordinator.sh/docs/best-practices/colocation-of-spark-jobs), there will be
  examples on running co-located workloads.

## Code of conduct

The Koordinator community is guided by our [Code of Conduct](CODE_OF_CONDUCT.md), which we encourage everybody to read
before participating.

In the interest of fostering an open and welcoming environment, we as contributors and maintainers pledge to making
participation in our project and our community a harassment-free experience for everyone, regardless of age, body size,
disability, ethnicity, gender identity and expression, level of experience, education, socio-economic status,
nationality, personal appearance, race, religion, or sexual identity and orientation.

## Contributing

You are warmly welcome to hack on Koordinator. We have prepared a detailed guide [CONTRIBUTING.md](CONTRIBUTING.md).

## Membership

We encourage all contributors to become members. We aim to grow an active, healthy community of contributors, reviewers,
and code owners. Learn more about requirements and responsibilities of membership in
our [Community Membership](docs/community/community-membership.md) page.

## Community

You can reach the maintainers of this project at:

- [Slack](https://join.slack.com/t/koordinator-sh/shared_invite/zt-1756qoub4-Cn4~esfdlfAPsD7cwO2NzA)
- DingTalk(Chinese): Search Group ID `33383887` or scan the following QR Code

<div>
  <img src="https://github.com/koordinator-sh/koordinator/raw/main/docs/images/dingtalk.png" width="300" alt="Dingtalk QRCode">
</div>

## License

Koordinator is licensed under the Apache License, Version 2.0. See [LICENSE](./LICENSE) for the full license text.

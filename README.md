# Koordinator

[![License](https://img.shields.io/badge/license-Apache%202-4EB1BA.svg)](https://www.apache.org/licenses/LICENSE-2.0.html)

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

## Contributing

You are warmly welcome to hack on Koordinator. We have prepared a detailed guide [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Koordinator is licensed under the Apache License, Version 2.0. See [LICENSE](./LICENSE) for the full license text.

## Community, discussion, contribution, and support

You can reach the maintainers of this project at:

- [Slack](https://join.slack.com/t/koordinator-sh/shared_invite/zt-1756qoub4-Cn4~esfdlfAPsD7cwO2NzA)
- DingTalk: Search Group ID `33383887` or scan the following QR Code

<div>
  <img src="https://user-images.githubusercontent.com/24452340/161946660-225da039-c5b3-4baf-ae49-12a94a5f3df1.jpg" width="300" alt="Dingtalk QRCode">
</div>
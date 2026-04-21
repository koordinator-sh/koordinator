# Hybrid Helm Chart

A Helm chart for deploying Hybrid Manager and Collector on Kubernetes.

## Prerequisites

- Kubernetes cluster (1.19+)
- Helm 3.x installed
- kubectl configured to access kubernetes cluster

## Installation

### Install into a specific namespace

```bash
helm upgrade --install --namespace hybrid-system hybrid ./hybrid/  --create-namespace --set config.prometheus.url=http://localhost:9090 --set aiCredentials.server=http://localhost:8000
```

### Install with custom values

```bash
helm upgrade --install --namespace hybrid-system hybrid ./hybrid/  --create-namespace -f custom-values.yaml
```



## Configuration

### Key Parameters

| Parameter | Description | Default                 |
|-----------|-------------|-------------------------|
| `namespace.name` | Namespace to deploy | `hybrid-system`         |
| `namespace.create` | Create namespace if not exists | `false`                 |
| `agent.replicas` | Number of manager replicas | `1`                     |
| `config.prometheus.url` | Prometheus server URL | `http://localhost:9090` |
| `persistence.enabled` | Enable persistent storage | `true`                  |
| `persistence.size` | PVC size | `10Gi`                  |
| `aiCredentials.server` | AI server URL | `http://localhost:8000` |
| `aiCredentials.token` | AI authentication token |

### AI Credentials

To configure AI credentials, update the `values.yaml`:

```yaml
aiCredentials:
  create: true
  server: "ai-server-url"
  token: "ai-server-token"
```

Or use existing secret:

```yaml
aiCredentials:
  create: false
  existingSecret: "exist-secret-name"
```

### Storage Configuration

To use a specific storage class:

```yaml
persistence:
  enabled: true
  storageClass: "" # Storage class name
  size: 10Gi
```

## Uninstall

```bash
helm uninstall hybrid --namespace hybrid-system
```

## Upgrading

```bash
helm upgrade hybrid ./hybrid --namespace hybrid-system
```

## Components

### Hybrid Agent

#### Hybrid Collector Model
- Collects metrics from Prometheus or VictoriaMetrics
- Exports data for AI analysis
- Supports CPU, Memory, I/O, and Network metrics

#### Hybrid Manager Model
- Manages Kubernetes workloads
- Syncs with AI server for intelligent decisions
- Requires RBAC permissions for pods, deployments, statefulsets, daemonsets, replicasets, jobs, and cronjobs


## Troubleshooting

### Check pod status

```bash
kubectl get pods -n hybrid-system
```

### View logs

```bash
kubectl logs -n hybrid-system deployment/hybrid-agent

```

### Check events

```bash
kubectl get events -n hybrid-system --sort-by='.lastTimestamp'
```

# ContainerCgroupOverride POC Runbook (phase-1 nested CR)

> Branch: `feat/container-cgroup-override`  
> Proposal: [20260909-container-cgroup-override.md](./20260909-container-cgroup-override.md)

## Goals

1. Create nested `ContainerCgroupOverride` → manager patches `NodeSLO.Extensions` → koordlet applies hard limits.
2. Delete CR → writeback baseline → `status.phase=Released` → finalizer removed.
3. `koordinator.sh/cgroup-override-skip=true` leaves the container alone (NRC coexistence).
4. Annotation path remains as fallback (flat or nested JSON).

## API shape (redesign)

```yaml
spec:
  target: { podName, containerName, podUID? }   # NRC-like identity
  resources:                                    # ACK-like nesting
    memory: { max }
    cpu: { quota, period?, cpuset? }
    blkio: { ... }                              # schema only for now
  writebackOnDelete: true                       # host baseline, not PodSpec
```

## Prerequisites

1. Install CRD:

```bash
kubectl apply -f config/crd/bases/slo.koordinator.sh_containercgroupoverrides.yaml
```

2. Deploy koord-manager (`containercgroup` controller) + koordlet:

```text
--qos-extension-plugins=ContainerCgroupOverride=true
--container-cgroup-override-interval=2s
```

3. Mutual exclusion: keep Burst off **or** set skip when NRC owns the Pod.

## CR path

```bash
kubectl apply -f docs/proposals/koordlet/examples/container-cgroup-override-cr.yaml
# edit target.podName / namespace to match a live Pod
```

Expect:

```bash
kubectl get cco -A
# Phase → Pending → Applied

kubectl get nodeslo <node> -o jsonpath='{.spec.extensions}' | jq .
```

Delete:

```bash
kubectl delete cco -n <ns> <name>
# writeback → Released → object gone
```

## Annotation fallback

```bash
# flat
kubectl annotate pod <pod> -n <ns> \
  koordinator.sh/cgroup-override='{"containerName":"main","memoryMax":"512Mi"}'

# nested
kubectl annotate pod <pod> -n <ns> \
  koordinator.sh/cgroup-override='{"containerName":"main","resources":{"memory":{"max":"512Mi"},"cpu":{"quota":"200m"}}}'
```

## Checklist

| # | Check | Pass |
|---|-------|------|
| 1 | CRD nested schema | |
| 2 | Manager patches Extensions with `resources` | |
| 3 | Apply memory/cpu cgroup files | |
| 4 | Delete restores baseline | |
| 5 | Skip annotation no-ops | |
| 6 | `go test ./pkg/koordlet/qosmanager/plugins/containercgroup/ ./pkg/slo-controller/containercgroup/` | |

## Rollback

1. Delete all CRs (wait Released).
2. Disable plugin flag.
3. Optional: delete CRD after residual scan.

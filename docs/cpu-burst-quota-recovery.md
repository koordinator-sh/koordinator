# CPU quota recovery

CPU Burst reconciles live cgroup quotas so external updates, including Kubernetes resource resize, cannot be hidden by the resource executor's write cache.

For multiple containers, the Pod quota accommodates the permitted child burst envelope. Each regular container and restartable init container (native sidecar) scales using its own throttling metric, subject to node load, the burst percentage and the duration limiter. Pod throttling alone does not identify which of several containers needs more CPU. An idle child therefore does not receive a quota increase merely because another child is busy.

The parent calculation includes ordinary init budgets and Pod overhead. An explicit Pod CPU limit remains a cap; child ceilings cannot exceed it. Unconstrained parent cgroups remain unconstrained. Ephemeral containers do not expand the parent burst envelope. Parent increases precede child updates; parent decreases follow them.

This reconciliation changes CPU cgroups, not Kubernetes resource requests or limits. VPA retains its own container support and Pod-level resource restrictions. Switching to `none` resets managed quotas to their configured baseline.

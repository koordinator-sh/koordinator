# Excluding namespaces from CPU Burst

Pass `--cpu-burst-excluded-namespaces=kube-system,example` to koordlet to skip CPU Burst updates for these namespaces. The default is empty, preserving existing behavior. Names are matched exactly; whitespace and empty comma-separated entries are ignored. Pod CPU Burst annotations cannot override this exclusion.

Excluded Pods remain visible to shared-pool load accounting, but CPU Burst does not update their Pod or container burst settings or CFS quotas. This differs from policy `none`, which can reset previously applied values. Other koordlet features are not disabled by this flag.

The flag is read at process startup. Adding a previously managed namespace does not restore its cgroups: restore the intended baseline before enabling the exclusion. Removing a namespace from the flag resumes CPU Burst reconciliation after koordlet restarts.

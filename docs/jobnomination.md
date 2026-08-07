# JobNomination Resource Retention Mechanism

## The Problem
Currently, when a Pod from a distributed/batch Job fails, its resources immediately become available to other workloads. Because replacement Pods from the same Job may experience scheduling delays, they can lose their previously assigned nodes. This loss of stability means large distributed Jobs may lose locality, and partial Job recovery becomes inefficient.

## The Solution: JobNomination Plugin
The `JobNomination` scheduler plugin introduces a lightweight resource retention mechanism that allows failed Pods belonging to the same Job to regain scheduling priority on previously used resources, without introducing a full reservation framework.

### Retention Flow
1. **Detection:** The plugin watches Pod events. When a Pod belonging to a Job transitions to a `Failed` phase, the plugin records its requested resources and the node it was running on.
2. **In-Memory Tracking:** The requested resources are tracked in an in-memory cache against the Job's UID and the Node's name. A bounded retention timeout (default: 2 minutes) is set for the nomination.
3. **Protection (Filter phase):** During the `Filter` phase, the plugin calculates the total resources retained by *other* jobs on the node and deducts them from the node's available resources. This prevents unrelated jobs from immediately consuming those retained resources during the retention window.
4. **Preference (Score phase):** During the `Score` phase, if a replacement Pod from the *same* Job is being scheduled, it receives a maximum score (100) for the node where its resources are retained.
5. **Consumption (Reserve phase):** When a replacement Pod is successfully reserved on the nominated node, its corresponding nomination is removed from the cache, preventing the Job from double-dipping retained resources.

### Fallback Behavior
- **Retention Expiration:** The tracking cache uses an expiration mechanism. After the 2-minute window, expired nominations are lazily dropped or cleaned up by a background goroutine. The node's resources then become fully available to all workloads.
- **Fairness:** The cluster will not be hard-locked globally because the retention window is strictly bounded.
- **Conflict Handling:** If a retained resource cannot be fulfilled because of other factors, the normal scheduler behaviors take over.

### Limitations of First Iteration
1. **In-Memory Only:** The state is purely in-memory within the koord-scheduler process. If the scheduler restarts, the tracking state is lost.
2. **Best-Effort Reservation:** The plugin currently implements retention by hiding resources from unrelated workloads. It does not enforce a rigid guarantee if the node's actual state drifts or if other pods were force-scheduled.
3. **Simple Timeouts:** Expirations are globally defined at plugin initialization and are not yet configurable per Job.
4. **Scope Control:** The current iteration does not involve multi-scheduler coordination or CRD-heavy persistent arbitration systems.

package metricsadvisor

import (
	"sync"
	"time"
)

const (
	nodeResUsedUpdateTime = "nodeResUsedUpdateTime"
	podResUsedUpdateTime  = "podResUsedUpdateTime"
	nodeCPUInfoUpdateTime = "nodeCPUInfoUpdateTime"
)

type collectState struct {
	mu            sync.RWMutex
	updateTimeMap map[string]*time.Time
}

func newCollectState() *collectState {
	return &collectState{
		mu: sync.RWMutex{},
		updateTimeMap: map[string]*time.Time{
			nodeResUsedUpdateTime: nil,
			podResUsedUpdateTime:  nil,
			nodeCPUInfoUpdateTime: nil,
		},
	}
}

func (c *collectState) HasSynced() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hasSynced := true
	for _, updateTime := range c.updateTimeMap {
		if updateTime == nil {
			hasSynced = false
			break
		}
	}

	return hasSynced
}

func (c *collectState) RefreshTime(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	totalTime := time.Now()
	c.updateTimeMap[key] = &totalTime
}

package frameworkext

import (
	"time"

	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	podSchedulingTraceKey = extension.SchedulingDomainPrefix + "/pod-scheduling-trace"
)

type PodSchedulingTrace struct {
	LastTracePointTime time.Time
	TimeLine           []tracePointInfo
}

type tracePointInfo struct {
	TracePointKey TracePointKey
	ElapsedTime   time.Duration
}

func (p *PodSchedulingTrace) Clone() framework.StateData {
	return p
}

type TracePointKey string

const (
	EnterIntoSchedulingCycle   TracePointKey = "EnterIntoSchedulingCycle"
	BeforeFirstBeforePreFilter TracePointKey = "BeforeFirstBeforePreFilter"
	AfterLastBeforePreFilter   TracePointKey = "AfterLastBeforePreFilter"
	AfterLastPreFilter         TracePointKey = "AfterLastPreFilter"
	AfterLastAfterPreFilter    TracePointKey = "AfterLastAfterPreFilter"
	BeforeReservationPreScore  TracePointKey = "BeforeReservationPreScore"
	AfterReservationPreScore   TracePointKey = "AfterReservationPreScore"
	BeforeFirstBeforeScore     TracePointKey = "BeforeFirstBeforeScore"
	AfterLastBeforeScore       TracePointKey = "AfterLastBeforeScore"
	AfterLastScore             TracePointKey = "AfterLastScore"
	BeforeFirstReserve         TracePointKey = "BeforeFirstReserve"
	AfterLastReserve           TracePointKey = "AfterLastReserve"
	BeforeFirstResize          TracePointKey = "BeforeFirstResize"
	AfterLastResize            TracePointKey = "AfterLastResize"
)

func (p *PodSchedulingTrace) AddNewTracePoint(key TracePointKey) {
	if p == nil {
		return
	}
	now := time.Now()
	p.TimeLine = append(p.TimeLine, tracePointInfo{
		TracePointKey: key,
		ElapsedTime:   now.Sub(p.LastTracePointTime),
	})
	p.LastTracePointTime = now
}

func InitPodSchedulingTrace(state *framework.CycleState) {
	if state == nil {
		return
	}
	state.Write(podSchedulingTraceKey, &PodSchedulingTrace{
		LastTracePointTime: time.Now(),
		TimeLine: []tracePointInfo{{
			TracePointKey: EnterIntoSchedulingCycle,
			ElapsedTime:   0,
		}},
	})
}

func GetPodSchedulingTrace(state *framework.CycleState) *PodSchedulingTrace {
	if state == nil {
		return nil
	}
	v, err := state.Read(podSchedulingTraceKey)
	if err != nil {
		return nil
	}
	s, ok := v.(*PodSchedulingTrace)
	if !ok || s == nil {
		return nil
	}
	return s
}

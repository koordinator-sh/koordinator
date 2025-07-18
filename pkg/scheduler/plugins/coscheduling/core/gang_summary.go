package core

import (
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
)

type GangSummary struct {
	Name                   string           `json:"name"`
	WaitTime               time.Duration    `json:"waitTime"`
	CreateTime             time.Time        `json:"createTime"`
	Mode                   string           `json:"mode"`
	GangMatchPolicy        string           `json:"gangMatchPolicy"`
	MinRequiredNumber      int              `json:"minRequiredNumber"`
	TotalChildrenNum       int              `json:"totalChildrenNum"`
	GangGroup              []string         `json:"gangGroup"`
	Children               sets.Set[string] `json:"children"`
	PendingChildren        sets.Set[string] `json:"pendingChildren"`
	WaitingForBindChildren sets.Set[string] `json:"waitingForBindChildren"`
	BoundChildren          sets.Set[string] `json:"boundChildren"`
	OnceResourceSatisfied  bool             `json:"onceResourceSatisfied"`
	GangGroupInfo          *GangGroupInfo   `json:"gangGroupInfo"`
	GangFrom               string           `json:"gangFrom"`
	HasGangInit            bool             `json:"hasGangInit"`
}

func (gang *Gang) GetGangSummary() *GangSummary {
	gangSummary := &GangSummary{
		Children:               sets.New[string](),
		PendingChildren:        sets.New[string](),
		WaitingForBindChildren: sets.New[string](),
		BoundChildren:          sets.New[string](),
	}

	if gang == nil {
		return gangSummary
	}

	gang.lock.RLock()
	defer gang.lock.RUnlock()

	gangSummary.Name = gang.Name
	gangSummary.WaitTime = gang.WaitTime
	gangSummary.CreateTime = gang.CreateTime
	gangSummary.Mode = gang.Mode
	gangSummary.GangMatchPolicy = gang.GangMatchPolicy
	gangSummary.MinRequiredNumber = gang.MinRequiredNumber
	gangSummary.TotalChildrenNum = gang.TotalChildrenNum
	gangSummary.OnceResourceSatisfied = gang.GangGroupInfo.isGangOnceResourceSatisfied()
	gangSummary.GangGroupInfo = gang.GangGroupInfo
	gangSummary.GangFrom = gang.GangFrom
	gangSummary.HasGangInit = gang.HasGangInit
	gangSummary.GangGroup = append(gangSummary.GangGroup, gang.GangGroup...)

	for podName := range gang.Children {
		gangSummary.Children.Insert(podName)
	}
	for podName := range gang.PendingChildren {
		gangSummary.PendingChildren.Insert(podName)
	}
	for podName := range gang.WaitingForBindChildren {
		gangSummary.WaitingForBindChildren.Insert(podName)
	}
	for podName := range gang.BoundChildren {
		gangSummary.BoundChildren.Insert(podName)
	}

	return gangSummary
}

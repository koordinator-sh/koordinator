/*
 Copyright 2022 The Koordinator Authors.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package reporter

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	listerbeta1 "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
)

var _ listerbeta1.NodeMetricLister = &fakeNodeMetricLister{}

type fakeNodeMetricLister struct {
	nodeMetrics *slov1alpha1.NodeMetric
}

func (f *fakeNodeMetricLister) List(selector labels.Selector) (ret []*slov1alpha1.NodeMetric, err error) {
	return []*slov1alpha1.NodeMetric{f.nodeMetrics}, nil
}

func (f *fakeNodeMetricLister) Get(name string) (*slov1alpha1.NodeMetric, error) {
	return f.nodeMetrics, nil
}

func Test_reporter_isNodeMetricInited(t *testing.T) {
	type fields struct {
		nodeMetric *slov1alpha1.NodeMetric
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "is-node-metric-inited",
			fields: fields{
				nodeMetric: &slov1alpha1.NodeMetric{},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &reporter{
				nodeMetric: tt.fields.nodeMetric,
			}
			if got := r.isNodeMetricInited(); got != tt.want {
				t.Errorf("isNodeMetricInited() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getNodeMetricReportInterval(t *testing.T) {
	nodeMetricLister := &fakeNodeMetricLister{
		nodeMetrics: &slov1alpha1.NodeMetric{
			Spec: slov1alpha1.NodeMetricSpec{
				CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
					ReportIntervalSeconds: pointer.Int64(666),
				},
			},
		},
	}
	r := &reporter{
		config:           NewDefaultConfig(),
		nodeMetricLister: nodeMetricLister,
	}
	reportInterval := r.getNodeMetricReportInterval()
	expectReportInterval := 666 * time.Second
	if reportInterval != expectReportInterval {
		t.Errorf("expect reportInterval %d but got %d", expectReportInterval, reportInterval)
	}
}

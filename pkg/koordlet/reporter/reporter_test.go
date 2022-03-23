package reporter

import (
	"testing"

	nodesv1beta1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_reporter_isNodeMetricInited(t *testing.T) {
	type fields struct {
		nodeMetric *nodesv1beta1.NodeMetric
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "is-node-metric-inited",
			fields: fields{
				nodeMetric: &nodesv1beta1.NodeMetric{},
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

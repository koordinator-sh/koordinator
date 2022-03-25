package metriccache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_fieldPercentileOfMetricList(t *testing.T) {
	type args struct {
		metricsList interface{}
		fieldName   string
		percentile  float32
	}
	tests := []struct {
		name    string
		args    args
		want    float64
		wantErr bool
	}{
		{
			name: "do not panic",
			args: args{
				metricsList: 1,
				fieldName:   "v",
				percentile:  0.5,
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "trow error for illegal percentile",
			args: args{
				metricsList: []struct {
					v float32
				}{},
				fieldName:  "v",
				percentile: -1,
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "trow error for illegal list length",
			args: args{
				metricsList: []struct {
					v float32
				}{},
				fieldName:  "v",
				percentile: 0.5,
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "trow error for illegal element type",
			args: args{
				metricsList: []struct {
					v int
				}{
					{v: 1000},
				},
				fieldName:  "v",
				percentile: 0.5,
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "calculate single-element list",
			args: args{
				metricsList: []struct {
					v float32
				}{
					{v: 1000},
				},
				fieldName:  "v",
				percentile: 0.5,
			},
			want:    1000,
			wantErr: false,
		},
		{
			name: "calculate multi-element list",
			args: args{
				metricsList: []struct {
					v float32
				}{
					{v: 800},
					{v: 100},
					{v: 200},
					{v: 600},
					{v: 700},
					{v: 0},
					{v: 500},
					{v: 400},
					{v: 300},
				},
				fieldName:  "v",
				percentile: 0.9,
			},
			want:    700,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fieldPercentileOfMetricList(tt.args.metricsList, AggregateParam{ValueFieldName: tt.args.fieldName}, tt.args.percentile)
			assert.Equal(t, true, tt.wantErr == (err != nil))
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_fieldLastOfMetricList(t *testing.T) {
	type args struct {
		metricsList interface{}
		param       AggregateParam
	}
	tests := []struct {
		name    string
		args    args
		want    float64
		wantErr bool
	}{
		{
			name: "do not panic for invalide metrics",
			args: args{
				metricsList: 1,
				param:       AggregateParam{ValueFieldName: "v", TimeFieldName: "T"},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "do not panic for invalid ValueFieldName",
			args: args{
				metricsList: []struct {
					v float32
					T time.Time
				}{
					{v: 1.0, T: time.Now().Add(-5 * time.Second)},
					{v: 2.0, T: time.Now()},
				},
				param: AggregateParam{TimeFieldName: "T"},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "do not panic for invalid TimeFieldName",
			args: args{
				metricsList: []struct {
					v float32
					t time.Time
				}{
					{v: 1.0, t: time.Now().Add(-5 * time.Second)},
					{v: 2.0, t: time.Now()},
				},
				param: AggregateParam{ValueFieldName: "v"},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "trow error for illegal time",
			args: args{
				metricsList: []struct {
					v float32
					t time.Time
				}{
					{v: 1.0, t: time.Now().Add(-5 * time.Second)},
					{v: 2.0, t: time.Now().Add(-3 * time.Second)},
					{v: 3.0, t: time.Now()},
				},
				param: AggregateParam{ValueFieldName: "v", TimeFieldName: "t"},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "trow error for illegal list length",
			args: args{
				metricsList: []struct {
					v float32
					T time.Time
				}{},
				param: AggregateParam{ValueFieldName: "v", TimeFieldName: "T"},
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "calculate single-element list",
			args: args{
				metricsList: []struct {
					v float32
					T time.Time
				}{
					{v: 3.0, T: time.Now()},
				},
				param: AggregateParam{ValueFieldName: "v", TimeFieldName: "T"},
			},
			want:    3.0,
			wantErr: false,
		},
		{
			name: "calculate multi-element list",
			args: args{
				metricsList: []struct {
					v float32
					T time.Time
				}{
					{v: 1.0, T: time.Now().Add(-5 * time.Second)},
					{v: 2.0, T: time.Now().Add(-3 * time.Second)},
					{v: 3.0, T: time.Now()},
				},
				param: AggregateParam{ValueFieldName: "v", TimeFieldName: "T"},
			},
			want:    3.0,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fieldLastOfMetricList(tt.args.metricsList, tt.args.param)
			assert.Equal(t, true, tt.wantErr == (err != nil))
			assert.Equal(t, tt.want, got)
		})
	}
}

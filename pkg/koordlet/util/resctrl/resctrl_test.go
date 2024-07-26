package resctrl

import (
	"fmt"
	"sync"
	"testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/stretchr/testify/assert"
)

func TestGetNewTaskIds(t *testing.T) {
	type args struct {
		ids      []int32
		tasksMap map[int32]struct{}
	}
	tests := []struct {
		name    string
		args    args
		want    []int32
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "tasksMap is nil",
			args: args{
				ids:      []int32{1, 2, 3},
				tasksMap: nil,
			},
			want:    []int32{1, 2, 3},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetNewTaskIds(tt.args.ids, tt.args.tasksMap)
			if !tt.wantErr(t, err, fmt.Sprintf("GetNewTaskIds(%v, %v)", tt.args.ids, tt.args.tasksMap)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetNewTaskIds(%v, %v)", tt.args.ids, tt.args.tasksMap)
		})
	}
}

func TestGetPodCgroupNewTaskIdsFromPodCtx(t *testing.T) {
	type args struct {
		podMeta  *protocol.PodContext
		tasksMap map[int32]struct{}
	}
	tests := []struct {
		name string
		args args
		want []int32
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, GetPodCgroupNewTaskIdsFromPodCtx(tt.args.podMeta, tt.args.tasksMap), "GetPodCgroupNewTaskIdsFromPodCtx(%v, %v)", tt.args.podMeta, tt.args.tasksMap)
		})
	}
}

func TestNewRDTEngine(t *testing.T) {
	type args struct {
		vendor string
	}
	tests := []struct {
		name    string
		args    args
		want    ResctrlEngine
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRDTEngine(tt.args.vendor)
			if !tt.wantErr(t, err, fmt.Sprintf("NewRDTEngine(%v)", tt.args.vendor)) {
				return
			}
			assert.Equalf(t, tt.want, got, "NewRDTEngine(%v)", tt.args.vendor)
		})
	}
}

func TestRDTEngine_GetApp(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	type args struct {
		id string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   App
		want1  bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			got, got1 := R.GetApp(tt.args.id)
			assert.Equalf(t, tt.want, got, "GetApp(%v)", tt.args.id)
			assert.Equalf(t, tt.want1, got1, "GetApp(%v)", tt.args.id)
		})
	}
}

func TestRDTEngine_GetApps(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	tests := []struct {
		name   string
		fields fields
		want   map[string]App
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			assert.Equalf(t, tt.want, R.GetApps(), "GetApps()")
		})
	}
}

func TestRDTEngine_ParseSchemata(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	type args struct {
		config extension.ResctrlConfig
		cbm    uint
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *sysutil.ResctrlSchemataRaw
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			assert.Equalf(t, tt.want, R.ParseSchemata(tt.args.config, tt.args.cbm), "ParseSchemata(%v, %v)", tt.args.config, tt.args.cbm)
		})
	}
}

func TestRDTEngine_Rebuild(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			R.Rebuild()
		})
	}
}

func TestRDTEngine_RegisterApp(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	type args struct {
		podid      string
		annotation string
		fromNRI    bool
		updater    ResctrlUpdater
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			tt.wantErr(t, R.RegisterApp(tt.args.podid, tt.args.annotation, tt.args.fromNRI, tt.args.updater), fmt.Sprintf("RegisterApp(%v, %v, %v, %v)", tt.args.podid, tt.args.annotation, tt.args.fromNRI, tt.args.updater))
		})
	}
}

func TestRDTEngine_UnRegisterApp(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	type args struct {
		podid   string
		fromNRI bool
		updater ResctrlUpdater
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			tt.wantErr(t, R.UnRegisterApp(tt.args.podid, tt.args.fromNRI, tt.args.updater), fmt.Sprintf("UnRegisterApp(%v, %v, %v)", tt.args.podid, tt.args.fromNRI, tt.args.updater))
		})
	}
}

func TestRDTEngine_calculateMba(t *testing.T) {
	type fields struct {
		Apps       map[string]App
		Cgm        ControlGroupManager
		CtrlGroups map[string]apiext.Resctrl
		l          sync.RWMutex
		CBM        uint
		Vendor     string
	}
	type args struct {
		mbaPercent int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int64
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			R := &RDTEngine{
				Apps:       tt.fields.Apps,
				Cgm:        tt.fields.Cgm,
				CtrlGroups: tt.fields.CtrlGroups,
				l:          tt.fields.l,
				CBM:        tt.fields.CBM,
				Vendor:     tt.fields.Vendor,
			}
			assert.Equalf(t, tt.want, R.calculateMba(tt.args.mbaPercent), "calculateMba(%v)", tt.args.mbaPercent)
		})
	}
}

func Test_calculateAMDMba(t *testing.T) {
	type args struct {
		mbaPercent int64
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, calculateAMDMba(tt.args.mbaPercent), "calculateAMDMba(%v)", tt.args.mbaPercent)
		})
	}
}

func Test_calculateIntelMba(t *testing.T) {
	type args struct {
		mbaPercent int64
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, calculateIntelMba(tt.args.mbaPercent), "calculateIntelMba(%v)", tt.args.mbaPercent)
		})
	}
}

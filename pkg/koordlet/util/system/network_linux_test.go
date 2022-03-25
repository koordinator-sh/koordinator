//go:build linux
// +build linux

package system

import (
	"fmt"
	"net"
	"testing"

	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
)

func Test_EnsureQosctlReady(t *testing.T) {
	type args struct {
		mockCmdFn func(cmds []string) ([]byte, int, error)
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "rpm is not installed",
			args: args{
				mockCmdFn: func(cmds []string) ([]byte, int, error) {
					if cmds[0] == "rpm" {
						return []byte("未安装软件包"), 1, fmt.Errorf("not installed")
					}
					return nil, 0, nil
				},
			},
			wantErr: true,
		},
		{
			name: "rpm is installed and KMods are loaded",
			args: args{
				mockCmdFn: func(cmds []string) ([]byte, int, error) {
					if cmds[0] == "rpm" {
						return []byte("aqos"), 0, nil
					} else if cmds[0] == "lsmod" {
						return []byte(`sch_sfq                24576  0
sch_dsmark             20480  0
sch_htb                24576  0`), 0, nil
					}
					return nil, 0, nil
				},
			},
			wantErr: false,
		},
		{
			name: "rpm is installed and KMods are not loaded",
			args: args{
				mockCmdFn: func(cmds []string) ([]byte, int, error) {
					if cmds[0] == "rpm" {
						return []byte("aqos"), 0, nil
					} else if cmds[0] == "lsmod" {
						return []byte(""), 0, nil
					}
					return nil, 0, nil
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubs := gostub.Stub(&ExecCmdOnHost, tt.args.mockCmdFn)
			defer stubs.Reset()
			assert.Equal(t, tt.wantErr, EnsureQosctlReady(&net.Interface{}) != nil)
		})
	}
}

func Test_HasBandwidthCtrlSetup(t *testing.T) {
	dummyIndex := 24
	dummyLink := netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Index: dummyIndex,
		},
	}
	t.Run("bandwidth control not setup", func(t *testing.T) {
		stubs := gostub.Stub(&netLinkByIndexFn, func(index int) (netlink.Link, error) {
			return &dummyLink, nil
		})
		stubs.Stub(&netLinkQdiscListFn, func(link netlink.Link) ([]netlink.Qdisc, error) {
			return nil, fmt.Errorf("qdisc not exist")
		})
		defer stubs.Reset()

		assert.Equal(t, false, HasBandwidthCtrlSetup(&net.Interface{
			Index: dummyIndex,
		}))
	})

	t.Run("bandwidth control is setup", func(t *testing.T) {
		stubs := gostub.Stub(&netLinkByIndexFn, func(index int) (netlink.Link, error) {
			return &dummyLink, nil
		})
		stubs.Stub(&netLinkQdiscListFn, func(link netlink.Link) ([]netlink.Qdisc, error) {
			return make([]netlink.Qdisc, BandwidthCtrlQdiscCount), nil
		})
		defer stubs.Reset()

		assert.Equal(t, true, HasBandwidthCtrlSetup(&net.Interface{
			Index: dummyIndex,
		}))
	})

}

func Test_ResetBandwidthCtrl(t *testing.T) {
	helper := NewNetworkTestUtil(t)
	defer helper.Cleanup()

	dummyLink := helper.SetupDummyLinkWithAddr()
	ResetBandwidthCtrl(&net.Interface{
		Index: dummyLink.Index,
	})

	afterQdiscs, err := netlink.QdiscList(dummyLink)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(afterQdiscs))
}

func Test_GetHostDefaultIface(t *testing.T) {
	helper := NewNetworkTestUtil(t)
	defer helper.Cleanup()

	dummyLink := helper.SetupDummyLinkWithAddr()
	netlink.RouteAdd(&netlink.Route{
		LinkIndex: dummyLink.Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst: &net.IPNet{
			IP:   net.IPv4zero,
			Mask: net.CIDRMask(0, 32),
		},
	})

	findIface := GetHostDefaultIface()
	assert.NotNil(t, findIface)

	expectLink, _ := netlink.LinkByName(dummyLink.Name)
	assert.Equal(t, expectLink.Attrs().Index, findIface.Index)
}

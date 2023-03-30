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

package util

import (
	"reflect"
	"testing"
)

func TestLocalStorageInfo_scanDevicesOutput(t *testing.T) {
	type fields struct {
		DiskNumberMap    map[string]string
		NumberDiskMap    map[string]string
		PartitionDiskMap map[string]string
		VGDiskMap        map[string]string
		LVMapperVGMap    map[string]string
		MPDiskMap        map[string]string
	}
	type args struct {
		output []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    *LocalStorageInfo
	}{
		{
			name: "test",
			fields: fields{
				DiskNumberMap:    make(map[string]string),
				NumberDiskMap:    make(map[string]string),
				PartitionDiskMap: make(map[string]string),
			},
			args: args{
				output: []byte(
					`NAME="vdd" TYPE="disk" MAJ:MIN="253:48"
					NAME="vdb" TYPE="disk" MAJ:MIN="253:16"
					NAME="vde" TYPE="disk" MAJ:MIN="253:64"
					NAME="vdc" TYPE="disk" MAJ:MIN="253:32"
					NAME="vdc2" TYPE="part" MAJ:MIN="253:34"
					NAME="vdc3" TYPE="part" MAJ:MIN="253:35"
					NAME="yoda--pool0-yoda--2c52d97f--eab6--4ac5--ba8b--242f399470e1" TYPE="lvm" MAJ:MIN="252:1"
					NAME="yoda--pool0-yoda--3d505e88--9d62--4a69--9fba--092402e4264b" TYPE="lvm" MAJ:MIN="252:4"
					NAME="yoda--pool0-yoda--28f38aba--c76e--4dd3--be00--449451393b9d" TYPE="lvm" MAJ:MIN="252:2"
					NAME="yoda--pool0-yoda--15199981--7229--45e9--b3a0--b5b30a6a162b" TYPE="lvm" MAJ:MIN="252:0"
					NAME="yoda--pool0-yoda--23da3223--0d5d--42a4--8235--1f0442b37af2" TYPE="lvm" MAJ:MIN="252:5"
					NAME="yoda--pool0-yoda--87d8625a--dcc9--47bf--a14a--994cf2971193" TYPE="lvm" MAJ:MIN="252:3"
					NAME="vdc1" TYPE="part" MAJ:MIN="253:33"
					NAME="vda" TYPE="disk" MAJ:MIN="253:0"
					NAME="vda1" TYPE="part" MAJ:MIN="253:1"`,
				),
			},
			want: &LocalStorageInfo{
				DiskNumberMap: map[string]string{
					"/dev/vda": "253:0",
					"/dev/vdb": "253:16",
					"/dev/vdc": "253:32",
					"/dev/vdd": "253:48",
					"/dev/vde": "253:64",
				},
				NumberDiskMap: map[string]string{
					"253:0":  "/dev/vda",
					"253:16": "/dev/vdb",
					"253:32": "/dev/vdc",
					"253:48": "/dev/vdd",
					"253:64": "/dev/vde",
				},
				PartitionDiskMap: map[string]string{
					"/dev/vda1": "/dev/vda",
					"/dev/vdc1": "/dev/vdc",
					"/dev/vdc2": "/dev/vdc",
					"/dev/vdc3": "/dev/vdc",
				},
				// MPDiskMap: map[string]string{},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &LocalStorageInfo{
				DiskNumberMap:    tt.fields.DiskNumberMap,
				NumberDiskMap:    tt.fields.NumberDiskMap,
				PartitionDiskMap: tt.fields.PartitionDiskMap,
				VGDiskMap:        tt.fields.VGDiskMap,
				LVMapperVGMap:    tt.fields.LVMapperVGMap,
				MPDiskMap:        tt.fields.MPDiskMap,
			}
			if err := s.scanDevicesOutput(tt.args.output); (err != nil) != tt.wantErr {
				t.Errorf("LocalStorageInfo.scanDevicesOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(s, tt.want) {
				t.Errorf("s(%v) and tt.want(%v) are not equal", s, tt.want)
			}
		})
	}
}

func TestLocalStorageInfo_scanVolumeGroupsOutput(t *testing.T) {
	type fields struct {
		DiskNumberMap    map[string]string
		NumberDiskMap    map[string]string
		PartitionDiskMap map[string]string
		VGDiskMap        map[string]string
		LVMapperVGMap    map[string]string
		MPDiskMap        map[string]string
	}
	type args struct {
		output []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    *LocalStorageInfo
	}{
		// TODO: Add test cases.
		{
			name: "test",
			fields: fields{
				VGDiskMap: make(map[string]string),
				PartitionDiskMap: map[string]string{
					"/dev/vdc3": "/dev/vdc",
				},
				DiskNumberMap: map[string]string{
					"/dev/vda": "253:0",
					"/dev/vdb": "253:16",
					"/dev/vdc": "253:32",
					"/dev/vdd": "253:48",
					"/dev/vde": "253:64",
				},
			},
			args: args{
				output: []byte(
					`hunbu        2 /dev/vdd
					hunbu        2 /dev/vde
					yoda-pool0   1 /dev/vdc3
					yoda-pool1   1 /dev/vdb`,
				),
			},
			want: &LocalStorageInfo{
				PartitionDiskMap: map[string]string{
					"/dev/vdc3": "/dev/vdc",
				},
				DiskNumberMap: map[string]string{
					"/dev/vda": "253:0",
					"/dev/vdb": "253:16",
					"/dev/vdc": "253:32",
					"/dev/vdd": "253:48",
					"/dev/vde": "253:64",
				},
				VGDiskMap: map[string]string{
					"yoda-pool0": "/dev/vdc",
					"yoda-pool1": "/dev/vdb",
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &LocalStorageInfo{
				DiskNumberMap:    tt.fields.DiskNumberMap,
				NumberDiskMap:    tt.fields.NumberDiskMap,
				PartitionDiskMap: tt.fields.PartitionDiskMap,
				VGDiskMap:        tt.fields.VGDiskMap,
				LVMapperVGMap:    tt.fields.LVMapperVGMap,
				MPDiskMap:        tt.fields.MPDiskMap,
			}
			if err := s.scanVolumeGroupsOutput(tt.args.output); (err != nil) != tt.wantErr {
				t.Errorf("LocalStorageInfo.scanVolumeGroupsOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(s, tt.want) {
				t.Errorf("s(%v) and tt.want(%v) are not equal", s, tt.want)
			}
		})
	}
}

func TestLocalStorageInfo_scanLogicalVolumesOutput(t *testing.T) {
	type fields struct {
		DiskNumberMap    map[string]string
		NumberDiskMap    map[string]string
		PartitionDiskMap map[string]string
		VGDiskMap        map[string]string
		LVMapperVGMap    map[string]string
		MPDiskMap        map[string]string
	}
	type args struct {
		output []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    *LocalStorageInfo
	}{
		// TODO: Add test cases.
		{
			name: "test",
			fields: fields{
				LVMapperVGMap: make(map[string]string),
			},
			args: args{
				output: []byte(
					`yoda-15199981-7229-45e9-b3a0-b5b30a6a162b yoda-pool0
					yoda-23da3223-0d5d-42a4-8235-1f0442b37af2 yoda-pool0
					yoda-28f38aba-c76e-4dd3-be00-449451393b9d yoda-pool0
					yoda-2c52d97f-eab6-4ac5-ba8b-242f399470e1 yoda-pool0
					yoda-3d505e88-9d62-4a69-9fba-092402e4264b yoda-pool0
					yoda-87d8625a-dcc9-47bf-a14a-994cf2971193 yoda-pool0`,
				),
			},
			want: &LocalStorageInfo{
				LVMapperVGMap: map[string]string{
					"/dev/mapper/yoda--pool0-yoda--2c52d97f--eab6--4ac5--ba8b--242f399470e1": "yoda-pool0",
					"/dev/mapper/yoda--pool0-yoda--3d505e88--9d62--4a69--9fba--092402e4264b": "yoda-pool0",
					"/dev/mapper/yoda--pool0-yoda--28f38aba--c76e--4dd3--be00--449451393b9d": "yoda-pool0",
					"/dev/mapper/yoda--pool0-yoda--15199981--7229--45e9--b3a0--b5b30a6a162b": "yoda-pool0",
					"/dev/mapper/yoda--pool0-yoda--23da3223--0d5d--42a4--8235--1f0442b37af2": "yoda-pool0",
					"/dev/mapper/yoda--pool0-yoda--87d8625a--dcc9--47bf--a14a--994cf2971193": "yoda-pool0",
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &LocalStorageInfo{
				DiskNumberMap:    tt.fields.DiskNumberMap,
				NumberDiskMap:    tt.fields.NumberDiskMap,
				PartitionDiskMap: tt.fields.PartitionDiskMap,
				VGDiskMap:        tt.fields.VGDiskMap,
				LVMapperVGMap:    tt.fields.LVMapperVGMap,
				MPDiskMap:        tt.fields.MPDiskMap,
			}
			if err := s.scanLogicalVolumesOutput(tt.args.output); (err != nil) != tt.wantErr {
				t.Errorf("LocalStorageInfo.scanLogicalVolumesOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(s, tt.want) {
				t.Errorf("s(%v) and tt.want(%v) are not equal", s, tt.want)
			}
		})
	}
}

func TestLocalStorageInfo_scanMountPointOutput(t *testing.T) {
	type fields struct {
		DiskNumberMap    map[string]string
		NumberDiskMap    map[string]string
		PartitionDiskMap map[string]string
		VGDiskMap        map[string]string
		LVMapperVGMap    map[string]string
		MPDiskMap        map[string]string
	}
	type args struct {
		output []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    *LocalStorageInfo
	}{
		// TODO: Add test cases.
		{
			name: "test",
			fields: fields{
				MPDiskMap: make(map[string]string),
			},
			args: args{
				output: []byte(
					`TARGET="/" SOURCE="/dev/vda1"
					TARGET="/var/lib/etcd" SOURCE="/dev/vdb"
					TARGET="/var/lib/docker" SOURCE="/dev/vdc1"
					TARGET="/var/lib/kubelet" SOURCE="/dev/vdc2"
					TARGET="/var/lib/kubelet/pods/d806ee8d-fe28-4995-a836-d2356d44ec5f/volumes/kubernetes.io~csi/yoda-2c52d97f-eab6-4ac5-ba8b-242f399470e1/mount" SOURCE="/dev/mapper/yoda--pool0-yoda--2c52d97f--eab6--4ac5--ba8b--242f399470e1"
					TARGET="/var/lib/kubelet/pods/51d75b14-007e-4e18-a17c-d055b53e3cbf/volumes/kubernetes.io~csi/yoda-28f38aba-c76e-4dd3-be00-449451393b9d/mount" SOURCE="/dev/mapper/yoda--pool0-yoda--28f38aba--c76e--4dd3--be00--449451393b9d"
					TARGET="/var/lib/kubelet/pods/a569c873-e3dd-48d7-ad33-be3659630426/volumes/kubernetes.io~csi/yoda-23da3223-0d5d-42a4-8235-1f0442b37af2/mount" SOURCE="/dev/mapper/yoda--pool0-yoda--23da3223--0d5d--42a4--8235--1f0442b37af2"
					TARGET="/var/lib/kubelet/pods/8d3b2627-8a67-4c6a-9eb9-6706b8c45454/volumes/kubernetes.io~csi/yoda-87d8625a-dcc9-47bf-a14a-994cf2971193/mount" SOURCE="/dev/mapper/yoda--pool0-yoda--87d8625a--dcc9--47bf--a14a--994cf2971193"
					TARGET="/var/lib/kubelet/pods/0e02c7f5-9200-45e1-ab98-d43245d0927b/volumes/kubernetes.io~csi/yoda-3d505e88-9d62-4a69-9fba-092402e4264b/mount" SOURCE="/dev/mapper/yoda--pool0-yoda--3d505e88--9d62--4a69--9fba--092402e4264b"
					TARGET="/var/lib/kubelet/pods/0ef5bd7a-aa83-4242-8597-c7ab4afaf356/volumes/kubernetes.io~csi/yoda-15199981-7229-45e9-b3a0-b5b30a6a162b/mount" SOURCE="/dev/mapper/yoda--pool0-yoda--15199981--7229--45e9--b3a0--b5b30a6a162b"`,
				),
			},
			wantErr: false,
			want: &LocalStorageInfo{
				MPDiskMap: map[string]string{
					"/":                "/dev/vda1",
					"/var/lib/etcd":    "/dev/vdb",
					"/var/lib/docker":  "/dev/vdc1",
					"/var/lib/kubelet": "/dev/vdc2",
					"/var/lib/kubelet/pods/d806ee8d-fe28-4995-a836-d2356d44ec5f/volumes/kubernetes.io~csi/yoda-2c52d97f-eab6-4ac5-ba8b-242f399470e1/mount": "/dev/mapper/yoda--pool0-yoda--2c52d97f--eab6--4ac5--ba8b--242f399470e1",
					"/var/lib/kubelet/pods/51d75b14-007e-4e18-a17c-d055b53e3cbf/volumes/kubernetes.io~csi/yoda-28f38aba-c76e-4dd3-be00-449451393b9d/mount": "/dev/mapper/yoda--pool0-yoda--28f38aba--c76e--4dd3--be00--449451393b9d",
					"/var/lib/kubelet/pods/a569c873-e3dd-48d7-ad33-be3659630426/volumes/kubernetes.io~csi/yoda-23da3223-0d5d-42a4-8235-1f0442b37af2/mount": "/dev/mapper/yoda--pool0-yoda--23da3223--0d5d--42a4--8235--1f0442b37af2",
					"/var/lib/kubelet/pods/8d3b2627-8a67-4c6a-9eb9-6706b8c45454/volumes/kubernetes.io~csi/yoda-87d8625a-dcc9-47bf-a14a-994cf2971193/mount": "/dev/mapper/yoda--pool0-yoda--87d8625a--dcc9--47bf--a14a--994cf2971193",
					"/var/lib/kubelet/pods/0e02c7f5-9200-45e1-ab98-d43245d0927b/volumes/kubernetes.io~csi/yoda-3d505e88-9d62-4a69-9fba-092402e4264b/mount": "/dev/mapper/yoda--pool0-yoda--3d505e88--9d62--4a69--9fba--092402e4264b",
					"/var/lib/kubelet/pods/0ef5bd7a-aa83-4242-8597-c7ab4afaf356/volumes/kubernetes.io~csi/yoda-15199981-7229-45e9-b3a0-b5b30a6a162b/mount": "/dev/mapper/yoda--pool0-yoda--15199981--7229--45e9--b3a0--b5b30a6a162b",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &LocalStorageInfo{
				DiskNumberMap:    tt.fields.DiskNumberMap,
				NumberDiskMap:    tt.fields.NumberDiskMap,
				PartitionDiskMap: tt.fields.PartitionDiskMap,
				VGDiskMap:        tt.fields.VGDiskMap,
				LVMapperVGMap:    tt.fields.LVMapperVGMap,
				MPDiskMap:        tt.fields.MPDiskMap,
			}
			if err := s.scanMountPointOutput(tt.args.output); (err != nil) != tt.wantErr {
				t.Errorf("LocalStorageInfo.scanMountPointOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(s, tt.want) {
				t.Errorf("s(%v) and tt.want(%v) are not equal", s, tt.want)
			}
		})
	}
}

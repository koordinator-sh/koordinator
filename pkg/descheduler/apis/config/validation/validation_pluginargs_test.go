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

package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config/v1alpha2"
)

func TestValidateMigrationControllerArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    *v1alpha2.MigrationControllerArgs
		wantErr bool
	}{
		{
			name:    "default args",
			args:    &v1alpha2.MigrationControllerArgs{},
			wantErr: false,
		},
		{
			name: "invalid evictQPS",
			args: &v1alpha2.MigrationControllerArgs{
				EvictQPS: &deschedulerconfig.Float64OrString{
					Type:   deschedulerconfig.String,
					StrVal: "xxxx",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid evictBurst",
			args: &v1alpha2.MigrationControllerArgs{
				EvictQPS: &deschedulerconfig.Float64OrString{
					Type:     deschedulerconfig.Float,
					FloatVal: 11,
				},
				EvictBurst: ptr.To[int32](0),
			},
			wantErr: true,
		},
		{
			name: "invalid labelSelector",
			args: &v1alpha2.MigrationControllerArgs{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"test/a/b/c": "123",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid maxConcurrentReconciles",
			args: &v1alpha2.MigrationControllerArgs{
				MaxConcurrentReconciles: ptr.To[int32](-1),
			},
			wantErr: true,
		},
		{
			name: "invalid defaultJobMode",
			args: &v1alpha2.MigrationControllerArgs{
				DefaultJobMode: "unsupportedMode",
			},
			wantErr: true,
		},
		{
			name: "invalid defaultJobTTL",
			args: &v1alpha2.MigrationControllerArgs{
				DefaultJobTTL: &metav1.Duration{Duration: -10 * time.Minute},
			},
			wantErr: true,
		},
		{
			name: "invalid skipEvictionGates",
			args: &v1alpha2.MigrationControllerArgs{
				SkipEvictionGates: []deschedulerconfig.EvictionGate{"NoSuchGate"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v1alpha2.SetDefaults_MigrationControllerArgs(tt.args)
			args := &deschedulerconfig.MigrationControllerArgs{}
			assert.NoError(t, v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(tt.args, args, nil))
			if err := ValidateMigrationControllerArgs(nil, args); (err != nil) != tt.wantErr {
				t.Errorf("ValidateMigrationControllerArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMigrationControllerArgs_MaxMigratingPerNamespace(t *testing.T) {
	testCases := []struct {
		maxMigratingPerNamespace *int32
		wantErr                  bool
	}{
		{
			maxMigratingPerNamespace: int32Ptr(10),
			wantErr:                  false,
		},
		{
			maxMigratingPerNamespace: int32Ptr(100),
			wantErr:                  false,
		},
		{
			maxMigratingPerNamespace: int32Ptr(0),
			wantErr:                  false,
		},
		{
			maxMigratingPerNamespace: int32Ptr(-1),
			wantErr:                  true,
		},
	}

	for _, tc := range testCases {
		argsDefault := &v1alpha2.MigrationControllerArgs{}
		v1alpha2.SetDefaults_MigrationControllerArgs(argsDefault)
		args := &deschedulerconfig.MigrationControllerArgs{}
		assert.NoError(t, v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(argsDefault, args, nil))
		args.MaxMigratingPerNamespace = tc.maxMigratingPerNamespace

		err := ValidateMigrationControllerArgs(nil, args)
		if tc.wantErr {
			assert.Error(t, err, "Expected an error for invalid MaxMigratingPerNamespace")
			assert.Contains(t, err.Error(), "maxMigratingPerNamespace should be greater or equal 0", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}

func TestValidateMigrationControllerArgs_MaxMigratingGlobally(t *testing.T) {
	testCases := []struct {
		maxMigratingGlobally *int32
		wantErr              bool
	}{
		{
			maxMigratingGlobally: int32Ptr(10),
			wantErr:              false,
		},
		{
			maxMigratingGlobally: int32Ptr(100),
			wantErr:              false,
		},
		{
			maxMigratingGlobally: int32Ptr(0),
			wantErr:              false,
		},
		{
			maxMigratingGlobally: int32Ptr(-1),
			wantErr:              true,
		},
	}

	for _, tc := range testCases {
		argsDefault := &v1alpha2.MigrationControllerArgs{}
		v1alpha2.SetDefaults_MigrationControllerArgs(argsDefault)
		args := &deschedulerconfig.MigrationControllerArgs{}
		assert.NoError(t, v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(argsDefault, args, nil))
		args.MaxMigratingGlobally = tc.maxMigratingGlobally

		err := ValidateMigrationControllerArgs(nil, args)
		if tc.wantErr {
			assert.Error(t, err, "Expected an error for invalid MaxMigratingGlobally")
			assert.Contains(t, err.Error(), "maxMigratingGlobally should be greater or equal 0", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}

func TestValidateMigrationControllerArgs_MaxMigratingPerNode(t *testing.T) {
	testCases := []struct {
		maxMigratingPerNode *int32
		wantErr             bool
	}{
		{
			maxMigratingPerNode: int32Ptr(10),
			wantErr:             false,
		},
		{
			maxMigratingPerNode: int32Ptr(100),
			wantErr:             false,
		},
		{
			maxMigratingPerNode: int32Ptr(0),
			wantErr:             false,
		},
		{
			maxMigratingPerNode: int32Ptr(-1),
			wantErr:             true,
		},
	}

	for _, tc := range testCases {
		argsDefault := &v1alpha2.MigrationControllerArgs{}
		v1alpha2.SetDefaults_MigrationControllerArgs(argsDefault)
		args := &deschedulerconfig.MigrationControllerArgs{}
		assert.NoError(t, v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(argsDefault, args, nil))
		args.MaxMigratingPerNode = tc.maxMigratingPerNode

		err := ValidateMigrationControllerArgs(nil, args)
		if tc.wantErr {
			assert.Error(t, err, "Expected an error for invalid MaxMigratingPerNode")
			assert.Contains(t, err.Error(), "maxMigratingPerNode should be greater or equal 0", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}

func TestValidateMigrationControllerArgs_MaxMigratingPerWorkload(t *testing.T) {
	testCases := []struct {
		maxMigratingPerWorkload *intstr.IntOrString
		wantErr                 bool
	}{
		{
			maxMigratingPerWorkload: intstrPtr(10),
			wantErr:                 false,
		},
		{
			maxMigratingPerWorkload: intstrPtr(100),
			wantErr:                 false,
		},
		{
			maxMigratingPerWorkload: intstrPtr(0),
			wantErr:                 false,
		},
		{
			maxMigratingPerWorkload: intstrPtr(-1), // we do not check the valid value
			wantErr:                 false,
		},
	}

	for _, tc := range testCases {
		argsDefault := &v1alpha2.MigrationControllerArgs{}
		v1alpha2.SetDefaults_MigrationControllerArgs(argsDefault)
		args := &deschedulerconfig.MigrationControllerArgs{}
		assert.NoError(t, v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(argsDefault, args, nil))
		args.MaxMigratingPerWorkload = tc.maxMigratingPerWorkload

		err := ValidateMigrationControllerArgs(nil, args)
		if tc.wantErr {
			assert.Error(t, err, "Expected an error for invalid MaxMigratingPerWorkload")
			assert.Contains(t, err.Error(), "maxMigratingPerWorkload should be greater or equal 0", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}

func TestValidateMigrationControllerArgs_MaxUnavailablePerWorkload(t *testing.T) {
	testCases := []struct {
		maxUnavailablePerWorkload *intstr.IntOrString
		wantErr                   bool
	}{
		{
			maxUnavailablePerWorkload: intstrPtr(10),
			wantErr:                   false,
		},
		{
			maxUnavailablePerWorkload: intstrPtr(100),
			wantErr:                   false,
		},
		{
			maxUnavailablePerWorkload: intstrPtr(0),
			wantErr:                   false,
		},
		{
			maxUnavailablePerWorkload: intstrPtr(-1), // we do not check the valid value
			wantErr:                   false,
		},
	}

	for _, tc := range testCases {
		argsDefault := &v1alpha2.MigrationControllerArgs{}
		v1alpha2.SetDefaults_MigrationControllerArgs(argsDefault)
		args := &deschedulerconfig.MigrationControllerArgs{}
		assert.NoError(t, v1alpha2.Convert_v1alpha2_MigrationControllerArgs_To_config_MigrationControllerArgs(argsDefault, args, nil))
		args.MaxUnavailablePerWorkload = tc.maxUnavailablePerWorkload

		err := ValidateMigrationControllerArgs(nil, args)
		if tc.wantErr {
			assert.Error(t, err, "Expected an error for invalid MaxUnavailablePerWorkload")
			assert.Contains(t, err.Error(), "maxUnavailablePerWorkload should be greater or equal 0", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}

// Helper functions for pointer creation
func int32Ptr(value int32) *int32 {
	return &value
}

func intstrPtr(val int) *intstr.IntOrString {
	value := intstr.FromInt(val)
	return &value
}

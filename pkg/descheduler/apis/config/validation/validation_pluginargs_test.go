package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config/v1alpha2"
)

func TestValidateRemovePodsViolatingNodeAffinityArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    *v1alpha2.RemovePodsViolatingNodeAffinityArgs
		wantErr bool
	}{
		{
			name:    "default args",
			args:    &v1alpha2.RemovePodsViolatingNodeAffinityArgs{},
			wantErr: false,
		},
		{
			name: "only include namespaces",
			args: &v1alpha2.RemovePodsViolatingNodeAffinityArgs{
				Namespaces: &v1alpha2.Namespaces{
					Include: []string{"test-1", "test-2"},
				},
			},
			wantErr: false,
		},
		{
			name: "only exclude namespaces",
			args: &v1alpha2.RemovePodsViolatingNodeAffinityArgs{
				Namespaces: &v1alpha2.Namespaces{
					Exclude: []string{"test-1", "test-2"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid namespaces",
			args: &v1alpha2.RemovePodsViolatingNodeAffinityArgs{
				Namespaces: &v1alpha2.Namespaces{
					Include: []string{"test-1", "test-2"},
					Exclude: []string{"test-1", "test-2"},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v1alpha2.SetDefaults_RemovePodsViolatingNodeAffinityArgs(tt.args)
			args := &deschedulerconfig.RemovePodsViolatingNodeAffinityArgs{}
			assert.NoError(t, v1alpha2.Convert_v1alpha2_RemovePodsViolatingNodeAffinityArgs_To_config_RemovePodsViolatingNodeAffinityArgs(tt.args, args, nil))
			if err := ValidateRemovePodsViolatingNodeAffinityArgs(nil, args); (err != nil) != tt.wantErr {
				t.Errorf("ValidateRemovePodsViolatingNodeAffinityArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

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
				MaxConcurrentReconciles: pointer.Int32(-1),
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

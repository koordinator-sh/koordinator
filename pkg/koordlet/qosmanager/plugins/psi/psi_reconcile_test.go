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

package psi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/operator"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestExtractOperatorsKeepsDefaultPSIThresholdsWhenThresholdIsNil(t *testing.T) {
	ops, err := extractOperators(&slov1alpha1.PSIStrategy{
		PSIExport: &slov1alpha1.PSIExportConfig{
			Enable: ptr.To(true),
		},
	})

	assert.NoError(t, err)
	if assert.Len(t, ops, 1) {
		exporter, ok := ops[0].(*operator.PSIExport)
		assert.True(t, ok)
		assert.Equal(t, operator.DefaultPSIExporter.(*operator.PSIExport).CPU, exporter.CPU)
		assert.Equal(t, operator.DefaultPSIExporter.(*operator.PSIExport).Memory, exporter.Memory)
		assert.Equal(t, operator.DefaultPSIExporter.(*operator.PSIExport).IO, exporter.IO)
	}
}

func TestPSIReconcileEnabled(t *testing.T) {
	reconcile := &psiReconcile{reconcileInterval: 10 * time.Second}

	defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.PSIReconcile, false)()
	assert.False(t, reconcile.Enabled())

	defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.PSIReconcile, true)()
	assert.True(t, reconcile.Enabled())

	reconcile.reconcileInterval = 0
	assert.False(t, reconcile.Enabled())
}

func TestInjectOperatorDependencies(t *testing.T) {
	fakeClient := clientsetfake.NewSimpleClientset()
	reconcile := &psiReconcile{kubeClient: fakeClient}
	exporter := &operator.PSIExport{}

	reconcile.injectOperatorDependencies(exporter)

	assert.NoError(t, exporter.Init())
}

func TestIsSupported(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	t.Cleanup(helper.Cleanup)
	helper.SetCgroupsV2(true)

	system.CPUAcctCPUPressureV2.WithSupported(true, "supported for test")
	system.CPUAcctMemoryPressureV2.WithSupported(true, "supported for test")
	system.CPUAcctIOPressureV2.WithSupported(true, "supported for test")
	system.MemoryHighV2.WithSupported(true, "supported for test")
	system.MemoryMinV2.WithSupported(true, "supported for test")
	system.CPUCFSQuotaV2.WithSupported(true, "supported for test")
	system.CPUSharesV2.WithSupported(true, "supported for test")

	reconcile := &psiReconcile{}
	strategy := &slov1alpha1.PSIStrategy{
		PSIExport:      &slov1alpha1.PSIExportConfig{Enable: ptr.To(true)},
		MemorySuppress: &slov1alpha1.MemorySuppressConfig{Enable: ptr.To(true)},
		GroupShare:     &slov1alpha1.GroupShareConfig{Enable: ptr.To(true)},
		BudgetBalance:  &slov1alpha1.BudgetBalanceConfig{Enable: ptr.To(true)},
	}

	supported, msg := reconcile.isSupported(strategy)
	assert.True(t, supported, msg)

	helper.SetCgroupsV2(false)
	supported, msg = reconcile.isSupported(strategy)
	assert.False(t, supported)
	assert.Contains(t, msg, "cgroup v2")

	helper.SetCgroupsV2(true)
	system.CPUAcctCPUPressureV2.WithSupported(false, "disabled for test")
	supported, msg = reconcile.isSupported(&slov1alpha1.PSIStrategy{
		PSIExport: &slov1alpha1.PSIExportConfig{Enable: ptr.To(true)},
	})
	assert.False(t, supported)
	assert.Contains(t, msg, string(system.CPUAcctCPUPressureName))
}

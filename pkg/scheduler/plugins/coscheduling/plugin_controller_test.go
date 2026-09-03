/*
Copyright 2022 The Koordinator Authors.
Copyright 2020 The Kubernetes Authors.

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

package coscheduling

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	kubefake "k8s.io/client-go/kubernetes/fake"

	fakepgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/controller"
)

func TestNewControllers_DefaultWorkers(t *testing.T) {
	pgClientSet := fakepgclientset.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	suit := newPluginTestSuit(t, nil, pgClientSet, cs)

	plugin, ok := suit.plugin.(*Coscheduling)
	if !ok {
		t.Fatalf("expected *Coscheduling, got %T", suit.plugin)
	}
	plugin.args = nil

	controllers, err := plugin.NewControllers()
	assert.NoError(t, err)
	assert.Len(t, controllers, 1)

	pgCtrl, ok := controllers[0].(*controller.PodGroupController)
	if !ok {
		t.Fatalf("expected *controller.PodGroupController, got %T", controllers[0])
	}
	assert.Equal(t, controller.PodGroupControllerName, pgCtrl.Name())
	assert.Equal(t, 1, getControllerWorkers(t, pgCtrl))
}

func TestNewControllers_CustomWorkers(t *testing.T) {
	pgClientSet := fakepgclientset.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	suit := newPluginTestSuit(t, nil, pgClientSet, cs)

	plugin, ok := suit.plugin.(*Coscheduling)
	if !ok {
		t.Fatalf("expected *Coscheduling, got %T", suit.plugin)
	}
	plugin.args.ControllerWorkers = 3

	controllers, err := plugin.NewControllers()
	assert.NoError(t, err)
	assert.Len(t, controllers, 1)

	pgCtrl, ok := controllers[0].(*controller.PodGroupController)
	if !ok {
		t.Fatalf("expected *controller.PodGroupController, got %T", controllers[0])
	}
	assert.Equal(t, 3, getControllerWorkers(t, pgCtrl))
}

func getControllerWorkers(t *testing.T, ctrl *controller.PodGroupController) int {
	t.Helper()

	value := reflect.ValueOf(ctrl).Elem().FieldByName("workers")
	if !value.IsValid() {
		t.Fatalf("workers field not found")
	}
	return int(value.Int())
}

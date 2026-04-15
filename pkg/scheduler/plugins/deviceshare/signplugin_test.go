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

package deviceshare

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

// Plugin must satisfy fwktype.SignPlugin so opportunistic batching does
// not fall back to disabled globally.
var _ fwktype.SignPlugin = &Plugin{}

func TestPlugin_SignPod(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	mkPod := func(name string, requests corev1.ResourceList) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name)},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:      "c",
					Resources: corev1.ResourceRequirements{Requests: requests},
				}},
			},
		}
	}

	t.Run("pod without device requests contributes nothing", func(t *testing.T) {
		pod := mkPod("plain", corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status.IsSuccess())
		assert.Empty(t, fragments)
	})

	t.Run("pod requesting GPU contributes a fragment", func(t *testing.T) {
		pod := mkPod("gpu", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.True(t, status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.DeviceShare.deviceRequests", fragments[0].Key)
		assert.NotNil(t, fragments[0].Value)
	})

	t.Run("identical GPU requests yield identical fragment values", func(t *testing.T) {
		a := mkPod("a", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("200")})
		b := mkPod("b", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("200")})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.Equal(t, fa, fb)
	})

	t.Run("different GPU amounts yield different fragment values", func(t *testing.T) {
		a := mkPod("a", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("100")})
		b := mkPod("b", corev1.ResourceList{apiext.ResourceGPU: resource.MustParse("200")})
		fa, _ := pl.SignPod(context.TODO(), a)
		fb, _ := pl.SignPod(context.TODO(), b)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("malformed device request opts the pod out of batching", func(t *testing.T) {
		// ValidatePercentageResource rejects values > 100 that are not
		// multiples of 100. 150 triggers that branch.
		pod := mkPod("bad", corev1.ResourceList{
			apiext.ResourceGPU: resource.MustParse("150"),
		})
		fragments, status := pl.SignPod(context.TODO(), pod)
		assert.False(t, status.IsSuccess(), "malformed requests should not produce a signature")
		assert.Equal(t, fwktype.Unschedulable, status.Code())
		assert.Nil(t, fragments)
	})
}

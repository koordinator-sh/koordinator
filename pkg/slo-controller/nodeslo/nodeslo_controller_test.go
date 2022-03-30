package nodeslo

import (
	"context"
	"testing"

	"k8s.io/utils/pointer"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
)

func TestNodeSLOReconciler_initNodeSLO(t *testing.T) {
	testingResourceThresholdStrategy := config.DefaultResourceThresholdStrategy()
	testingResourceThresholdStrategy.Enable = pointer.BoolPtr(true)
	testingResourceThresholdStrategy.CPUSuppressThresholdPercent = pointer.Int64Ptr(60)
	type args struct {
		node    *corev1.Node
		nodeSLO *slov1alpha1.NodeSLO
	}
	type fields struct {
		client client.Client
	}
	tests := []struct {
		name    string
		args    args
		fields  fields
		want    *slov1alpha1.NodeSLOSpec
		wantErr bool
	}{
		{
			name: "no client",
			args: args{
				node:    &corev1.Node{},
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			fields:  fields{client: nil},
			want:    &slov1alpha1.NodeSLOSpec{},
			wantErr: true,
		},
		{
			name: "throw an error if no slo configmap",
			args: args{
				node:    &corev1.Node{},
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			fields: fields{client: fake.NewFakeClient()},
			want: &slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: config.DefaultResourceThresholdStrategy(),
			},
			wantErr: false,
		},
		{
			name: "unmarshal failed, use the default",
			args: args{
				node:    &corev1.Node{},
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			fields: fields{client: fake.NewFakeClient(&corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"invalidField\",\"cpuSuppressThresholdPercent\":60}}",
					config.SLOCtrlConfigMap:           "{\"clusterStrategy\":{\"invalidField\"}}",
				},
			})},
			want: &slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: config.DefaultResourceThresholdStrategy(),
			},
			wantErr: false,
		},
		{
			name: "get spec successfully",
			args: args{
				node:    &corev1.Node{},
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			fields: fields{client: fake.NewFakeClient(&corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
				},
			})},
			want: &slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: testingResourceThresholdStrategy,
			},
			wantErr: false,
		},
		{
			name: "get spec successfully 1",
			args: args{
				node:    &corev1.Node{},
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			fields: fields{client: fake.NewFakeClient(&corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
				},
			})},
			want: &slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: testingResourceThresholdStrategy,
			},
			wantErr: false,
		},
		{
			name: "get spec successfully from old qos config",
			args: args{
				node:    &corev1.Node{},
				nodeSLO: &slov1alpha1.NodeSLO{},
			},
			fields: fields{client: fake.NewFakeClient(&corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
				},
			})},
			want: &slov1alpha1.NodeSLOSpec{
				ResourceUsedThresholdWithBE: testingResourceThresholdStrategy,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctr := &NodeSLOReconciler{Client: tt.fields.client}
			err := ctr.initNodeSLO(tt.args.node, tt.args.nodeSLO)
			got := &tt.args.nodeSLO.Spec
			if (err != nil) != tt.wantErr {
				t.Errorf("NodeSLOReconciler.initNodeSLO() gotErr = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNodeSLOReconciler_Reconcile(t *testing.T) {
	// initial variants
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	slov1alpha1.AddToScheme(scheme)
	r := &NodeSLOReconciler{
		Client: fake.NewFakeClientWithScheme(scheme),
		Log:    ctrl.Log.WithName("controllers").WithName("NodeSLO"),
		Scheme: scheme,
	}
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	testingConfigMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.SLOCtrlConfigMap,
			Namespace: config.ConfigNameSpace,
		},
		Data: map[string]string{
			config.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":false,\"cpuSuppressThresholdPercent\":60}}",
		},
	}
	testingResourceThresholdStrategy := config.DefaultResourceThresholdStrategy()
	testingResourceThresholdStrategy.CPUSuppressThresholdPercent = pointer.Int64Ptr(60)

	nodeSLOSpec := &slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: testingResourceThresholdStrategy,
	}
	nodeReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: testingNode.Name}}
	// the NodeSLO does not exists before getting created
	nodeSLO := &slov1alpha1.NodeSLO{}
	err := r.Client.Get(context.TODO(), nodeReq.NamespacedName, nodeSLO)
	if !errors.IsNotFound(err) {
		t.Errorf("the testing NodeSLO should not exist before getting created, err: %s", err)
	}
	// throw an error if the configmap does not exist
	err = r.Client.Create(context.TODO(), testingNode)
	assert.NoError(t, err)
	_, err = r.Reconcile(context.TODO(), nodeReq)
	assert.NoError(t, err)
	// create and init a NodeSLO cr if the Node and the configmap exists
	err = r.Client.Create(context.TODO(), testingConfigMap)
	assert.NoError(t, err)
	_, err = r.Reconcile(context.TODO(), nodeReq)
	assert.NoError(t, err)
	nodeSLO = &slov1alpha1.NodeSLO{}
	err = r.Client.Get(context.TODO(), nodeReq.NamespacedName, nodeSLO)
	assert.NoError(t, err)
	assert.Equal(t, *nodeSLOSpec, nodeSLO.Spec)
	// delete the NodeSLO cr if the node no longer exists
	err = r.Delete(context.TODO(), testingNode)
	assert.NoError(t, err)
	_, err = r.Reconcile(context.TODO(), nodeReq)
	assert.NoError(t, err)
	nodeSLO = &slov1alpha1.NodeSLO{}
	err = r.Client.Get(context.TODO(), nodeReq.NamespacedName, nodeSLO)
	if !errors.IsNotFound(err) {
		t.Errorf("the testing NodeSLO should not exist after the Node is deleted, err: %s", err)
	}
}

package statesinformer

//import (
//	"github.com/golang/mock/gomock"
//	mock_client2 "github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler/mockclient"
//	"github.com/stretchr/testify/assert"
//	"go.uber.org/atomic"
//	corev1 "k8s.io/api/core/v1"
//	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
//	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
//	"testing"
//	"time"
//)
//
//const (
//	testPodID = "testPodID"
//)
//
//func Test_containerInformer_syncContainers(t *testing.T) {
//	stopCh := make(chan struct{}, 1)
//	defer close(stopCh)
//	ctl := gomock.NewController(t)
//	defer ctl.Finish()
//	c := NewDefaultConfig()
//	c.CRISyncInterval = 60 * time.Second
//	m := &containerInformer{
//		config:               c,
//		containerUpdatedTime: time.Time{},
//		containerMap:         make(map[string]map[string]*ContainerMeta),
//		containerHasSynced:   atomic.NewBool(false),
//		containerCreated:     make(chan createEvent, 1),
//		pleg:                 nil,
//	}
//	mockContainerMaps := make(map[string]map[string]*ContainerMeta)
//	mockContainerMaps[testPodID] = make(map[string]*ContainerMeta)
//
//	mockRuntimeClient := mock_client2.NewMockRuntimeServiceClient(ctl)
//	mockRuntimeClient.EXPECT().ListContainers(gomock.Any(), gomock.Any()).Return(&runtimeapi.ListContainersResponse{
//		Containers: []*runtimeapi.Container{
//			{
//				Id:           "c1",
//				PodSandboxId: testPodID,
//			},
//		},
//	}, nil)
//	mockRuntimeClient.EXPECT().ContainerStatus(gomock.Any(), gomock.Any()).Return(&runtimeapi.ContainerStatusResponse{
//		Status: &runtimeapi.ContainerStatus{Id: "c1"},
//	}, nil)
//	m.runtimeServiceClient = mockRuntimeClient
//
//	err := m.syncContainers()
//	assert.NoError(t, err)
//	if len(m.GetAllSandboxContainers()) != 1 {
//		t.Fatal("failed to update containers")
//	}
//}
//
//func Test_containerInformer_Run(t *testing.T) {
//	type fields struct {
//		config *Config
//		node   corev1.Node
//	}
//	tests := []struct {
//		name    string
//		fields  fields
//		wantErr bool
//	}{
//		{
//			name: "run with default config",
//			fields: fields{
//				config: NewDefaultConfig(),
//				node: corev1.Node{
//					ObjectMeta: metav1.ObjectMeta{
//						Name: "test-node-name",
//					},
//					Status: corev1.NodeStatus{
//						Addresses: []corev1.NodeAddress{
//							{
//								Type:    corev1.NodeInternalIP,
//								Address: "192.168.0.1",
//							},
//						},
//					},
//				},
//			},
//			wantErr: false,
//		},
//	}
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//
//		})
//	}
//}

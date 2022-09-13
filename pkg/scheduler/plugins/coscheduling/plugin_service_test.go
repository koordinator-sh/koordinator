package coscheduling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	fakepgclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config/v1beta2"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/core"
)

func newPluginTestSuitForGangAPI(t *testing.T, nodes []*corev1.Node) *pluginTestSuit {
	var v1beta2args v1beta2.CoschedulingArgs
	v1beta2.SetDefaults_CoschedulingArgs(&v1beta2args)
	var gangSchedulingArgs config.CoschedulingArgs
	err := v1beta2.Convert_v1beta2_CoschedulingArgs_To_config_CoschedulingArgs(&v1beta2args, &gangSchedulingArgs, nil)
	assert.NoError(t, err)

	pgClientSet := fakepgclientset.NewSimpleClientset()
	proxyNew := GangPluginFactoryProxy(pgClientSet, New)
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nodes)
	fh, err := schedulertesting.NewFramework(
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)
	return &pluginTestSuit{
		Handle:             fh,
		proxyNew:           proxyNew,
		gangSchedulingArgs: &gangSchedulingArgs,
		pgClient:           pgClientSet,
	}
}
func TestEndpointsQueryGangInfo(t *testing.T) {
	suit := newPluginTestSuitForGangAPI(t, nil)
	podToCreateGangA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ganga_ns",
			Name:      "pod1",
			Annotations: map[string]string{
				extension.AnnotationGangName:   "ganga",
				extension.AnnotationGangMinNum: "2",
			},
		},
	}
	_, err := suit.Handle.ClientSet().CoreV1().Pods("ganga_ns").Create(context.TODO(), podToCreateGangA, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("retry podClient create pod err: %v", err)
	}
	p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	suit.start()
	gp := p.(*Coscheduling)
	gangExpected := core.Gang{
		Name:              "ganga_ns/ganga",
		WaitTime:          time.Second * 600,
		CreateTime:        podToCreateGangA.CreationTimestamp.Time,
		Mode:              extension.GangModeStrict,
		MinRequiredNumber: 2,
		TotalChildrenNum:  2,
		Children: map[string]*corev1.Pod{
			"ganga_ns/pod1": podToCreateGangA,
		},
		GangGroup:                make([]string, 0),
		WaitingForBindChildren:   map[string]*corev1.Pod{},
		BoundChildren:            map[string]*corev1.Pod{},
		OnceResourceSatisfied:    false,
		ScheduleCycleValid:       true,
		ScheduleCycle:            1,
		ChildrenScheduleRoundMap: map[string]int{},
		GangFrom:                 core.GangFromPodAnnotation,
		HasGangInit:              true,
	}
	engine := gin.Default()
	gp.RegisterEndpoints(engine.Group("/"))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/gang/ganga_ns/ganga", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	gang := &core.Gang{}
	err = json.NewDecoder(w.Result().Body).Decode(gang)
	assert.NoError(t, err)
	assert.Equal(t, &gangExpected, gang)
}

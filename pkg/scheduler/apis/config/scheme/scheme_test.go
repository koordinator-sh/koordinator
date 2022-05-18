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

package scheme

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/apis/config/testing/defaults"

	"sigs.k8s.io/scheduler-plugins/apis/config"
	"sigs.k8s.io/scheduler-plugins/apis/config/v1beta2"
	"sigs.k8s.io/scheduler-plugins/pkg/coscheduling"
	"sigs.k8s.io/scheduler-plugins/pkg/noderesources"
	"sigs.k8s.io/scheduler-plugins/pkg/preemptiontoleration"
	"sigs.k8s.io/scheduler-plugins/pkg/trimaran/loadvariationriskbalancing"
	"sigs.k8s.io/scheduler-plugins/pkg/trimaran/targetloadpacking"
	"sigs.k8s.io/yaml"
)

var testCPUQuantity, _ = resource.ParseQuantity("1000m")

// TestCodecsDecodePluginConfig tests that embedded plugin args get decoded
// into their appropriate internal types and defaults are applied.
func TestCodecsDecodePluginConfig(t *testing.T) {
	testCPUQuantity, _ := resource.ParseQuantity("1000m")
	testCases := []struct {
		name         string
		data         []byte
		wantErr      string
		wantProfiles []schedconfig.KubeSchedulerProfile
	}{
		// v1beta2 tests
		{
			name: "v1beta2 all plugin args in default profile",
			data: []byte(`
apiVersion: kubescheduler.config.k8s.io/v1beta2
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: scheduler-plugins
  pluginConfig:
  - name: Coscheduling
    args:
      permitWaitingTimeSeconds: 10
      deniedPGExpirationTimeSeconds: 3
  - name: NodeResourcesAllocatable
    args:
      mode: Least
      resources:
      - name: cpu
        weight: 1000000
      - name: memory
        weight: 1
  - name: TargetLoadPacking
    args:
      targetUtilization: 60 
      defaultRequests:
        cpu: "1000m"
      defaultRequestsMultiplier: "1.8"
      watcherAddress: http://deadbeef:2020
      metricProvider: 
        type: Prometheus
        address: http://prometheus-k8s.monitoring.svc.cluster.local:9090
  - name: LoadVariationRiskBalancing
    args:
      metricProvider:
        type: Prometheus
        address: http://prometheus-k8s.monitoring.svc.cluster.local:9090
        insecureSkipVerify: false
      safeVarianceMargin: 1.0
      safeVarianceSensitivity: 1.0
      watcherAddress: http://deadbeef:2020
  - name: PreemptionToleration
    args:
      minCandidateNodesPercentage: 20
      minCandidateNodesAbsolute: 200
`),
			wantProfiles: []schedconfig.KubeSchedulerProfile{
				{
					SchedulerName: "scheduler-plugins",
					Plugins:       defaults.PluginsV1beta2,
					PluginConfig: []schedconfig.PluginConfig{
						{
							Name: coscheduling.Name,
							Args: &config.CoschedulingArgs{
								PermitWaitingTimeSeconds:      10,
								DeniedPGExpirationTimeSeconds: 3,
							},
						},
						{
							Name: noderesources.AllocatableName,
							Args: &config.NodeResourcesAllocatableArgs{
								Mode: config.Least,
								Resources: []schedconfig.ResourceSpec{
									{Name: string(corev1.ResourceCPU), Weight: 1000000},
									{Name: string(corev1.ResourceMemory), Weight: 1},
								},
							},
						},
						{
							Name: targetloadpacking.Name,
							Args: &config.TargetLoadPackingArgs{
								TargetUtilization: 60,
								DefaultRequests: corev1.ResourceList{
									corev1.ResourceCPU: testCPUQuantity,
								},
								DefaultRequestsMultiplier: "1.8",
								WatcherAddress:            "http://deadbeef:2020",
								MetricProvider: config.MetricProviderSpec{
									Type:               config.Prometheus,
									Address:            "http://prometheus-k8s.monitoring.svc.cluster.local:9090",
									InsecureSkipVerify: true,
								},
							},
						},
						{
							Name: loadvariationriskbalancing.Name,
							Args: &config.LoadVariationRiskBalancingArgs{
								SafeVarianceMargin:      v1beta2.DefaultSafeVarianceMargin,
								SafeVarianceSensitivity: v1beta2.DefaultSafeVarianceSensitivity,
								WatcherAddress:          "http://deadbeef:2020",
								MetricProvider: config.MetricProviderSpec{
									Type:               config.Prometheus,
									Address:            "http://prometheus-k8s.monitoring.svc.cluster.local:9090",
									InsecureSkipVerify: false,
								},
							},
						},
						{
							Name: preemptiontoleration.Name,
							Args: &config.PreemptionTolerationArgs{MinCandidateNodesPercentage: 20, MinCandidateNodesAbsolute: 200},
						},
						{
							Name: "DefaultPreemption",
							Args: &schedconfig.DefaultPreemptionArgs{MinCandidateNodesPercentage: 10, MinCandidateNodesAbsolute: 100},
						},
						{
							Name: "InterPodAffinity",
							Args: &schedconfig.InterPodAffinityArgs{HardPodAffinityWeight: 1},
						},
						{
							Name: "NodeAffinity",
							Args: &schedconfig.NodeAffinityArgs{},
						},
						{
							Name: "NodeResourcesBalancedAllocation",
							Args: &schedconfig.NodeResourcesBalancedAllocationArgs{Resources: []schedconfig.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}}},
						},
						{
							Name: "NodeResourcesFit",
							Args: &schedconfig.NodeResourcesFitArgs{
								ScoringStrategy: &schedconfig.ScoringStrategy{
									Type:      schedconfig.LeastAllocated,
									Resources: []schedconfig.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}},
								},
							},
						},
						{
							Name: "PodTopologySpread",
							Args: &schedconfig.PodTopologySpreadArgs{DefaultingType: schedconfig.SystemDefaulting},
						},
						{
							Name: "VolumeBinding",
							Args: &schedconfig.VolumeBindingArgs{BindTimeoutSeconds: 600},
						},
					},
				},
			},
		},
		{
			name: "v1beta2 plugin args unspecified to verify the default profile",
			data: []byte(`
apiVersion: kubescheduler.config.k8s.io/v1beta2
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: scheduler-plugins
  pluginConfig:
  - name: Coscheduling
    args:
  - name: NodeResourcesAllocatable
    args:
  - name: TargetLoadPacking
    args:
  - name: LoadVariationRiskBalancing
    args:
  - name: PreemptionToleration
    args:
`),
			wantProfiles: []schedconfig.KubeSchedulerProfile{
				{
					SchedulerName: "scheduler-plugins",
					Plugins:       defaults.PluginsV1beta2,
					PluginConfig: []schedconfig.PluginConfig{
						{
							Name: coscheduling.Name,
							Args: &config.CoschedulingArgs{
								PermitWaitingTimeSeconds:      60,
								DeniedPGExpirationTimeSeconds: 20,
							},
						},
						{
							Name: noderesources.AllocatableName,
							Args: &config.NodeResourcesAllocatableArgs{
								Mode: config.Least,
								Resources: []schedconfig.ResourceSpec{
									{Name: string(corev1.ResourceCPU), Weight: 1048576},
									{Name: string(corev1.ResourceMemory), Weight: 1},
								},
							},
						},
						{
							Name: targetloadpacking.Name,
							Args: &config.TargetLoadPackingArgs{
								TargetUtilization: 40,
								DefaultRequests: corev1.ResourceList{
									corev1.ResourceCPU: testCPUQuantity,
								},
								DefaultRequestsMultiplier: "1.5",
								WatcherAddress:            "",
								MetricProvider: config.MetricProviderSpec{
									Type:    config.KubernetesMetricsServer,
									Address: "",
									Token:   "",
								},
							},
						},
						{
							Name: loadvariationriskbalancing.Name,
							Args: &config.LoadVariationRiskBalancingArgs{
								SafeVarianceMargin:      v1beta2.DefaultSafeVarianceMargin,
								SafeVarianceSensitivity: v1beta2.DefaultSafeVarianceSensitivity,
								WatcherAddress:          "",
								MetricProvider: config.MetricProviderSpec{
									Type:    config.KubernetesMetricsServer,
									Address: "",
									Token:   "",
								},
							},
						},
						{
							Name: preemptiontoleration.Name,
							Args: &config.PreemptionTolerationArgs{MinCandidateNodesPercentage: 10, MinCandidateNodesAbsolute: 100},
						},
						{
							Name: "DefaultPreemption",
							Args: &schedconfig.DefaultPreemptionArgs{MinCandidateNodesPercentage: 10, MinCandidateNodesAbsolute: 100},
						},
						{
							Name: "InterPodAffinity",
							Args: &schedconfig.InterPodAffinityArgs{HardPodAffinityWeight: 1},
						},
						{
							Name: "NodeAffinity",
							Args: &schedconfig.NodeAffinityArgs{},
						},
						{
							Name: "NodeResourcesBalancedAllocation",
							Args: &schedconfig.NodeResourcesBalancedAllocationArgs{Resources: []schedconfig.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}}},
						},
						{
							Name: "NodeResourcesFit",
							Args: &schedconfig.NodeResourcesFitArgs{
								ScoringStrategy: &schedconfig.ScoringStrategy{
									Type:      schedconfig.LeastAllocated,
									Resources: []schedconfig.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}},
								},
							},
						},
						{
							Name: "PodTopologySpread",
							Args: &schedconfig.PodTopologySpreadArgs{DefaultingType: schedconfig.SystemDefaulting},
						},
						{
							Name: "VolumeBinding",
							Args: &schedconfig.VolumeBindingArgs{BindTimeoutSeconds: 600},
						},
					},
				},
			},
		},
		{
			name: "v1beta2 coscheduling plugin args illegal to get validation error",
			data: []byte(`
apiVersion: kubescheduler.config.k8s.io/v1beta2
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: scheduler-plugins
  pluginConfig:
  - name: Coscheduling
    args:
      kubeConfigPath: "/var/run/kubernetes/kube.config"
`),
			wantErr: `decoding .profiles[0].pluginConfig[0]: decoding args for plugin Coscheduling: strict decoding error: unknown field "kubeConfigPath"`,
		},
	}
	decoder := Codecs.UniversalDecoder()
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			obj, gvk, err := decoder.Decode(tt.data, nil, nil)
			if err != nil {
				if tt.wantErr != err.Error() {
					t.Fatalf("\ngot err:\n\t%v\nwant:\n\t%s", err, tt.wantErr)
				}
				return
			}
			if len(tt.wantErr) != 0 {
				t.Fatalf("no error produced, wanted %v", tt.wantErr)
			}
			got, ok := obj.(*schedconfig.KubeSchedulerConfiguration)
			if !ok {
				t.Fatalf("decoded into %s, want %s", gvk, config.SchemeGroupVersion.WithKind("KubeSchedulerConfiguration"))
			}
			if diff := cmp.Diff(tt.wantProfiles, got.Profiles); diff != "" {
				t.Errorf("unexpected configuration (-want,+got):\n%s", diff)
			}
		})
	}
}

func TestCodecsEncodePluginConfig(t *testing.T) {
	testCases := []struct {
		name    string
		obj     runtime.Object
		version schema.GroupVersion
		want    string
	}{
		// v1beta2 tests
		{
			name:    "v1beta2 plugins",
			version: v1beta2.SchemeGroupVersion,
			obj: &schedconfig.KubeSchedulerConfiguration{
				Profiles: []schedconfig.KubeSchedulerProfile{
					{
						SchedulerName: "scheduler-plugins",
						PluginConfig: []schedconfig.PluginConfig{
							{
								Name: coscheduling.Name,
								Args: &config.CoschedulingArgs{
									PermitWaitingTimeSeconds:      10,
									DeniedPGExpirationTimeSeconds: 3,
								},
							},
							{
								Name: noderesources.AllocatableName,
								Args: &config.NodeResourcesAllocatableArgs{
									Mode: config.Least,
									Resources: []schedconfig.ResourceSpec{
										{Name: string(corev1.ResourceCPU), Weight: 1000000},
										{Name: string(corev1.ResourceMemory), Weight: 1},
									},
								},
							},
							{
								Name: targetloadpacking.Name,
								Args: &config.TargetLoadPackingArgs{
									TargetUtilization: 60,
									DefaultRequests: corev1.ResourceList{
										corev1.ResourceCPU: testCPUQuantity,
									},
									DefaultRequestsMultiplier: "1.8",
									WatcherAddress:            "http://deadbeef:2020",
									MetricProvider: config.MetricProviderSpec{
										Type:    config.Prometheus,
										Address: "http://prometheus-k8s.monitoring.svc.cluster.local:9090",
									},
								},
							},
							{
								Name: loadvariationriskbalancing.Name,
								Args: &config.LoadVariationRiskBalancingArgs{
									SafeVarianceMargin:      v1beta2.DefaultSafeVarianceMargin,
									SafeVarianceSensitivity: v1beta2.DefaultSafeVarianceSensitivity,
									WatcherAddress:          "http://deadbeef:2020",
									MetricProvider: config.MetricProviderSpec{
										Type:               config.Prometheus,
										Address:            "http://prometheus-k8s.monitoring.svc.cluster.local:9090",
										InsecureSkipVerify: false,
									},
								},
							},
						},
					},
				},
			},
			want: `apiVersion: kubescheduler.config.k8s.io/v1beta2
clientConnection:
  acceptContentTypes: ""
  burst: 0
  contentType: ""
  kubeconfig: ""
  qps: 0
enableContentionProfiling: false
enableProfiling: false
healthzBindAddress: ""
kind: KubeSchedulerConfiguration
leaderElection:
  leaderElect: false
  leaseDuration: 0s
  renewDeadline: 0s
  resourceLock: ""
  resourceName: ""
  resourceNamespace: ""
  retryPeriod: 0s
metricsBindAddress: ""
parallelism: 0
percentageOfNodesToScore: 0
podInitialBackoffSeconds: 0
podMaxBackoffSeconds: 0
profiles:
- pluginConfig:
  - args:
      apiVersion: kubescheduler.config.k8s.io/v1beta2
      deniedPGExpirationTimeSeconds: 3
      kind: CoschedulingArgs
      permitWaitingTimeSeconds: 10
    name: Coscheduling
  - args:
      apiVersion: kubescheduler.config.k8s.io/v1beta2
      kind: NodeResourcesAllocatableArgs
      mode: Least
      resources:
      - name: cpu
        weight: 1000000
      - name: memory
        weight: 1
    name: NodeResourcesAllocatable
  - args:
      apiVersion: kubescheduler.config.k8s.io/v1beta2
      defaultRequests:
        cpu: "1"
      defaultRequestsMultiplier: "1.8"
      kind: TargetLoadPackingArgs
      metricProvider:
        address: http://prometheus-k8s.monitoring.svc.cluster.local:9090
        insecureSkipVerify: false
        token: ""
        type: Prometheus
      targetUtilization: 60
      watcherAddress: http://deadbeef:2020
    name: TargetLoadPacking
  - args:
      apiVersion: kubescheduler.config.k8s.io/v1beta2
      kind: LoadVariationRiskBalancingArgs
      metricProvider:
        address: http://prometheus-k8s.monitoring.svc.cluster.local:9090
        insecureSkipVerify: false
        token: ""
        type: Prometheus
      safeVarianceMargin: 1
      safeVarianceSensitivity: 1
      watcherAddress: http://deadbeef:2020
    name: LoadVariationRiskBalancing
  schedulerName: scheduler-plugins
`,
		},
	}
	yamlInfo, ok := runtime.SerializerInfoForMediaType(Codecs.SupportedMediaTypes(), runtime.ContentTypeYAML)
	if !ok {
		t.Fatalf("unable to locate encoder -- %q is not a supported media type", runtime.ContentTypeYAML)
	}
	jsonInfo, ok := runtime.SerializerInfoForMediaType(Codecs.SupportedMediaTypes(), runtime.ContentTypeJSON)
	if !ok {
		t.Fatalf("unable to locate encoder -- %q is not a supported media type", runtime.ContentTypeJSON)
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			encoder := Codecs.EncoderForVersion(yamlInfo.Serializer, tt.version)
			var buf bytes.Buffer
			if err := encoder.Encode(tt.obj, &buf); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, buf.String()); diff != "" {
				t.Errorf("unexpected encoded configuration: (-want,+got)\n%s", diff)
			}
			encoder = Codecs.EncoderForVersion(jsonInfo.Serializer, tt.version)
			buf = bytes.Buffer{}
			if err := encoder.Encode(tt.obj, &buf); err != nil {
				t.Fatal(err)
			}
			out, err := yaml.JSONToYAML(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, string(out)); diff != "" {
				t.Errorf("unexpected encoded configuration: (-want,+got)\n%s", diff)
			}
		})
	}
}

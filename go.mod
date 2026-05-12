module github.com/koordinator-sh/koordinator

go 1.25.0

require (
	github.com/Mellanox/rdmamap v1.1.0
	github.com/NVIDIA/go-nvml v0.11.6-0.0.20220823120812-7e2082095e82
	github.com/cakturk/go-netstat v0.0.0-20200220111822-e5b49efee7a5
	github.com/containerd/nri v0.11.0
	github.com/coreos/go-iptables v0.5.0
	github.com/docker/docker v28.2.2+incompatible
	github.com/evanphx/json-patch v5.6.0+incompatible
	github.com/fsnotify/fsnotify v1.9.0
	github.com/gin-gonic/gin v1.9.0
	github.com/go-kit/log v0.2.1
	github.com/go-logr/logr v1.4.3
	github.com/go-playground/locales v0.14.1
	github.com/go-playground/universal-translator v0.18.1
	github.com/go-playground/validator/v10 v10.11.2
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8
	github.com/golang/protobuf v1.5.4
	github.com/google/btree v1.1.3
	github.com/google/go-cmp v0.7.0
	github.com/google/renameio v0.1.0
	github.com/google/uuid v1.6.0
	github.com/hodgesds/perf-utils v0.7.0
	github.com/jaypipes/ghw v0.20.0
	github.com/jedib0t/go-pretty/v6 v6.4.0
	github.com/k8stopologyawareschedwg/noderesourcetopology-api v0.1.1
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826
	github.com/mwitkow/grpc-proxy v0.0.0-20230212185441-f345521cb9c9
	github.com/onsi/ginkgo/v2 v2.27.2
	github.com/onsi/gomega v1.38.2
	github.com/opencontainers/runc v1.3.3
	github.com/openkruise/kruise-api v1.5.0
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/prashantv/gostub v1.1.0
	github.com/prometheus/client_golang v1.23.2
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/prometheus v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.11.1
	github.com/urfave/cli/v2 v2.27.7
	github.com/vishvananda/netlink v1.3.1
	go.uber.org/atomic v1.11.0
	go.uber.org/mock v0.6.0
	go.uber.org/multierr v1.11.0
	golang.org/x/crypto v0.48.0
	golang.org/x/net v0.51.0
	golang.org/x/sys v0.41.0
	golang.org/x/time v0.14.0
	google.golang.org/grpc v1.79.1
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.35.2
	k8s.io/apimachinery v0.35.2
	k8s.io/apiserver v0.35.2
	k8s.io/client-go v0.35.2
	k8s.io/code-generator v0.35.2
	k8s.io/component-base v0.35.2
	k8s.io/component-helpers v0.35.2
	k8s.io/cri-api v0.35.2
	k8s.io/gengo v0.0.0-20220902162205-c0856e24416d
	k8s.io/klog/v2 v2.140.0
	k8s.io/kube-scheduler v0.35.2
	k8s.io/kubectl v0.35.2
	k8s.io/kubelet v0.35.2
	k8s.io/kubernetes v1.35.2
	k8s.io/metrics v0.35.2
	k8s.io/utils v0.0.0-20260108192941-914a6e750570
	sigs.k8s.io/controller-runtime v0.23.1
	sigs.k8s.io/controller-runtime/tools/setup-envtest v0.0.0-20231005234617-5771399a8ce5
	sigs.k8s.io/descheduler v0.35.1
	sigs.k8s.io/yaml v1.6.0
)

require (
	cel.dev/expr v0.25.1 // indirect
	cyphar.com/go-pathrs v0.2.4 // indirect
	github.com/Azure/azure-sdk-for-go v68.0.0+incompatible // indirect
	github.com/Azure/go-ansiterm v0.0.0-20230124172434-306776ec8161 // indirect
	github.com/Azure/go-autorest/autorest v0.11.29 // indirect
	github.com/Azure/go-autorest/autorest/adal v0.9.23 // indirect
	github.com/JeffAshton/win_pdh v0.0.0-20161109143554-76bb4ee9f0ab // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Microsoft/hnslib v0.1.2 // indirect
	github.com/NYTimes/gziphandler v1.1.1 // indirect
	github.com/alecthomas/units v0.0.0-20240927000941-0f3dac36c52b // indirect
	github.com/antlr4-go/antlr/v4 v4.13.0 // indirect
	github.com/armon/circbuf v0.0.0-20190214190532-5111143e8da2 // indirect
	github.com/aws/aws-sdk-go v1.44.102 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/bytedance/sonic v1.8.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20221115062448-fe3a3abad311 // indirect
	github.com/cilium/ebpf v0.17.3 // indirect
	github.com/container-storage-interface/spec v1.9.0 // indirect
	github.com/containerd/cgroups/v3 v3.1.3 // indirect
	github.com/containerd/containerd/api v1.10.0 // indirect
	github.com/containerd/containerd/v2 v2.2.1 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/ttrpc v1.2.7 // indirect
	github.com/containerd/typeurl/v2 v2.2.3 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.6.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/cyphar/filepath-securejoin v0.6.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dennwc/varint v1.0.0 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/euank/go-kmsg-parser v2.0.0+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.0 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/cadvisor v0.53.0 // indirect
	github.com/google/cel-go v0.26.0 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/google/pprof v0.0.0-20250403155104-27863c87afa6 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/grafana/regexp v0.0.0-20220304095617-2e8d9baf4ac2 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jaypipes/pcidb v1.1.1 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/karrick/godirwalk v1.17.0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/knqyf263/go-plugin v0.9.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/libopenstorage/openstorage v1.0.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mistifyio/go-zfs v2.1.2-0.20190413222219-f784269be439+incompatible // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/spdystream v0.5.0 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/opencontainers/cgroups v0.0.4 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/opencontainers/selinux v1.13.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/common/sigv4 v0.1.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/spf13/afero v1.10.0 // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tetratelabs/wazero v1.10.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.9 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.etcd.io/etcd/api/v3 v3.6.5 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.6.5 // indirect
	go.etcd.io/etcd/client/v3 v3.6.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/github.com/emicklei/go-restful/otelrestful v0.44.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.66.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.66.0 // indirect
	go.opentelemetry.io/otel v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.41.0 // indirect
	go.opentelemetry.io/otel/metric v1.41.0 // indirect
	go.opentelemetry.io/otel/sdk v1.41.0 // indirect
	go.opentelemetry.io/otel/trace v1.41.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	go.uber.org/goleak v1.3.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/exp v0.0.0-20241108190413-2d47ceb2692f // indirect
	golang.org/x/mod v0.32.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/term v0.40.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	golang.org/x/tools v0.41.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v1.0.2-0.20250314012144-ee69052608d9 // indirect
	k8s.io/apiextensions-apiserver v0.35.2 // indirect
	k8s.io/cloud-provider v0.35.2 // indirect
	k8s.io/controller-manager v0.35.2 // indirect
	k8s.io/cri-client v0.35.2 // indirect
	k8s.io/csi-translation-lib v0.35.2 // indirect
	k8s.io/dynamic-resource-allocation v0.35.2 // indirect
	k8s.io/gengo/v2 v2.0.0-20250922181213-ec3ebc5fd46b // indirect
	k8s.io/kms v0.35.2 // indirect
	k8s.io/kube-openapi v0.30.0 // indirect
	k8s.io/mount-utils v0.35.2 // indirect
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.31.2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.2-0.20260122202528-d9cc6641c482 // indirect
)

replace (
	github.com/docker/distribution => github.com/docker/distribution v2.8.2+incompatible
	// github.com/containerd/ttrpc => github.com/containerd/ttrpc v1.1.0
	github.com/go-jose/go-jose => github.com/go-jose/go-jose v4.1.4+incompatible
	github.com/go-jose/go-jose/v4 => github.com/go-jose/go-jose/v4 v4.1.4
	github.com/go-logr/logr => github.com/go-logr/logr v1.4.3
	github.com/gophercloud/gophercloud => github.com/gophercloud/gophercloud v0.1.0
	github.com/grpc-ecosystem/go-grpc-middleware => github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/go-grpc-prometheus => github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/grpc-ecosystem/grpc-gateway => github.com/grpc-ecosystem/grpc-gateway v1.16.0
	github.com/grpc-ecosystem/grpc-gateway/v2 => github.com/grpc-ecosystem/grpc-gateway/v2 v2.26.3
	github.com/koordinator-sh/apis => github.com/koordinator-sh/apis v1.2.0
	github.com/prometheus/prometheus => github.com/prometheus/prometheus v0.39.2
	google.golang.org/api => google.golang.org/api v0.114.0
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20230526161137-0005af68ea54
	google.golang.org/genproto/googleapis/api => google.golang.org/genproto/googleapis/api v0.0.0-20250303144028-a0af3efb3deb
	google.golang.org/genproto/googleapis/rpc => google.golang.org/genproto/googleapis/rpc v0.0.0-20250528174236-200df99c418a
	google.golang.org/grpc => google.golang.org/grpc v1.79.1
	google.golang.org/protobuf => google.golang.org/protobuf v1.36.11
	k8s.io/api => k8s.io/api v0.35.2
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.35.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.35.2
	k8s.io/apiserver => k8s.io/apiserver v0.35.2
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.35.2
	k8s.io/client-go => k8s.io/client-go v0.35.2
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.35.2
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.35.2
	k8s.io/code-generator => k8s.io/code-generator v0.35.2
	k8s.io/component-base => k8s.io/component-base v0.35.2
	k8s.io/component-helpers => k8s.io/component-helpers v0.35.2
	k8s.io/controller-manager => k8s.io/controller-manager v0.35.2
	k8s.io/cri-api => k8s.io/cri-api v0.35.2
	k8s.io/cri-client => k8s.io/cri-client v0.35.2
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.35.2
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.35.2
	k8s.io/endpointslice => k8s.io/endpointslice v0.35.2
	k8s.io/externaljwt => k8s.io/externaljwt v0.35.2
	k8s.io/gengo/v2 => k8s.io/gengo/v2 v2.0.0-20250922181213-ec3ebc5fd46b
	k8s.io/klog/v2 => k8s.io/klog/v2 v2.130.1
	k8s.io/kms => k8s.io/kms v0.35.2
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.35.2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.35.2
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20250910181357-589584f1c912
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.35.2
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.35.2
	k8s.io/kubectl => k8s.io/kubectl v0.35.2
	k8s.io/kubelet => k8s.io/kubelet v0.35.2
	k8s.io/kubernetes => k8s.io/kubernetes v1.35.2
	k8s.io/metrics => k8s.io/metrics v0.35.2
	k8s.io/mount-utils => k8s.io/mount-utils v0.35.2
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.35.2
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.35.2
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.23.1
)

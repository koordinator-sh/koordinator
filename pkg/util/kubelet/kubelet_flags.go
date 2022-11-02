/*
Copyright 2022 The Koordinator Authors.
Copyright 2017 The Kubernetes Authors.

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

package kubelet

import (
	"strings"

	"github.com/spf13/pflag"
	cliflag "k8s.io/component-base/cli/flag"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	utilflag "k8s.io/kubernetes/pkg/util/flag"
)

// TODO(@joseph.t.lee): In the future, we should consider switching to the /configz query configuration provided by kubelet.

// AddKubeletConfigFlags adds flags for a specific kubeletconfig.KubeletConfiguration to the specified FlagSet
func AddKubeletConfigFlags(mainfs *pflag.FlagSet, c *kubeletconfiginternal.KubeletConfiguration) {
	fs := pflag.NewFlagSet("", pflag.ExitOnError)
	defer func() {
		// All KubeletConfiguration flags are now deprecated, and any new flags that point to
		// KubeletConfiguration fields are deprecated-on-creation. When removing flags at the end
		// of their deprecation period, be careful to check that they have *actually* been deprecated
		// members of the KubeletConfiguration for the entire deprecation period:
		// e.g. if a flag was added after this deprecation function, it may not be at the end
		// of its lifetime yet, even if the rest are.
		deprecated := "This parameter should be set via the config file specified by the Kubelet's --config flag. See https://kubernetes.io/docs/tasks/administer-cluster/kubelet-config-file/ for more information."
		fs.VisitAll(func(f *pflag.Flag) {
			f.Deprecated = deprecated
		})
		mainfs.AddFlagSet(fs)
	}()

	fs.BoolVar(&c.EnableServer, "enable-server", c.EnableServer, "Enable the Kubelet's server")

	fs.BoolVar(&c.FailSwapOn, "fail-swap-on", c.FailSwapOn, "Makes the Kubelet fail to start if swap is enabled on the node. ")
	fs.StringVar(&c.StaticPodPath, "pod-manifest-path", c.StaticPodPath, "Path to the directory containing static pod files to run, or the path to a single static pod file. Files starting with dots will be ignored.")
	fs.DurationVar(&c.SyncFrequency.Duration, "sync-frequency", c.SyncFrequency.Duration, "Max period between synchronizing running containers and config")
	fs.DurationVar(&c.FileCheckFrequency.Duration, "file-check-frequency", c.FileCheckFrequency.Duration, "Duration between checking config files for new data")
	fs.DurationVar(&c.HTTPCheckFrequency.Duration, "http-check-frequency", c.HTTPCheckFrequency.Duration, "Duration between checking http for new data")
	fs.StringVar(&c.StaticPodURL, "manifest-url", c.StaticPodURL, "URL for accessing additional Pod specifications to run")
	fs.Var(cliflag.NewColonSeparatedMultimapStringString(&c.StaticPodURLHeader), "manifest-url-header", "Comma-separated list of HTTP headers to use when accessing the url provided to --manifest-url. Multiple headers with the same name will be added in the same order provided. This flag can be repeatedly invoked. For example: --manifest-url-header 'a:hello,b:again,c:world' --manifest-url-header 'b:beautiful'")
	fs.Var(utilflag.IPVar{Val: &c.Address}, "address", "The IP address for the Kubelet to serve on (set to '0.0.0.0' or '::' for listening in all interfaces and IP families)")
	fs.Int32Var(&c.Port, "port", c.Port, "The port for the Kubelet to serve on.")
	fs.Int32Var(&c.ReadOnlyPort, "read-only-port", c.ReadOnlyPort, "The read-only port for the Kubelet to serve on with no authentication/authorization (set to 0 to disable)")

	// Authentication
	fs.BoolVar(&c.Authentication.Anonymous.Enabled, "anonymous-auth", c.Authentication.Anonymous.Enabled, ""+
		"Enables anonymous requests to the Kubelet server. Requests that are not rejected by another "+
		"authentication method are treated as anonymous requests. Anonymous requests have a username "+
		"of system:anonymous, and a group name of system:unauthenticated.")
	fs.BoolVar(&c.Authentication.Webhook.Enabled, "authentication-token-webhook", c.Authentication.Webhook.Enabled, ""+
		"Use the TokenReview API to determine authentication for bearer tokens.")
	fs.DurationVar(&c.Authentication.Webhook.CacheTTL.Duration, "authentication-token-webhook-cache-ttl", c.Authentication.Webhook.CacheTTL.Duration, ""+
		"The duration to cache responses from the webhook token authenticator.")
	fs.StringVar(&c.Authentication.X509.ClientCAFile, "client-ca-file", c.Authentication.X509.ClientCAFile, ""+
		"If set, any request presenting a client certificate signed by one of the authorities in the client-ca-file "+
		"is authenticated with an identity corresponding to the CommonName of the client certificate.")

	// Authorization
	fs.StringVar((*string)(&c.Authorization.Mode), "authorization-mode", string(c.Authorization.Mode), ""+
		"Authorization mode for Kubelet server. Valid options are AlwaysAllow or Webhook. "+
		"Webhook mode uses the SubjectAccessReview API to determine authorization.")
	fs.DurationVar(&c.Authorization.Webhook.CacheAuthorizedTTL.Duration, "authorization-webhook-cache-authorized-ttl", c.Authorization.Webhook.CacheAuthorizedTTL.Duration, ""+
		"The duration to cache 'authorized' responses from the webhook authorizer.")
	fs.DurationVar(&c.Authorization.Webhook.CacheUnauthorizedTTL.Duration, "authorization-webhook-cache-unauthorized-ttl", c.Authorization.Webhook.CacheUnauthorizedTTL.Duration, ""+
		"The duration to cache 'unauthorized' responses from the webhook authorizer.")

	fs.StringVar(&c.TLSCertFile, "tls-cert-file", c.TLSCertFile, ""+
		"File containing x509 Certificate used for serving HTTPS (with intermediate certs, if any, concatenated after server cert). "+
		"If --tls-cert-file and --tls-private-key-file are not provided, a self-signed certificate and key "+
		"are generated for the public address and saved to the directory passed to --cert-dir.")
	fs.StringVar(&c.TLSPrivateKeyFile, "tls-private-key-file", c.TLSPrivateKeyFile, "File containing x509 private key matching --tls-cert-file.")
	fs.BoolVar(&c.ServerTLSBootstrap, "rotate-server-certificates", c.ServerTLSBootstrap, "Auto-request and rotate the kubelet serving certificates by requesting new certificates from the kube-apiserver when the certificate expiration approaches. Requires the RotateKubeletServerCertificate feature gate to be enabled, and approval of the submitted CertificateSigningRequest objects.")

	tlsCipherPreferredValues := cliflag.PreferredTLSCipherNames()
	tlsCipherInsecureValues := cliflag.InsecureTLSCipherNames()
	fs.StringSliceVar(&c.TLSCipherSuites, "tls-cipher-suites", c.TLSCipherSuites,
		"Comma-separated list of cipher suites for the server. "+
			"If omitted, the default Go cipher suites will be used. \n"+
			"Preferred values: "+strings.Join(tlsCipherPreferredValues, ", ")+". \n"+
			"Insecure values: "+strings.Join(tlsCipherInsecureValues, ", ")+".")
	tlsPossibleVersions := cliflag.TLSPossibleVersions()
	fs.StringVar(&c.TLSMinVersion, "tls-min-version", c.TLSMinVersion,
		"Minimum TLS version supported. "+
			"Possible values: "+strings.Join(tlsPossibleVersions, ", "))
	fs.BoolVar(&c.RotateCertificates, "rotate-certificates", c.RotateCertificates, "<Warning: Beta feature> Auto rotate the kubelet client certificates by requesting new certificates from the kube-apiserver when the certificate expiration approaches.")

	fs.Int32Var(&c.RegistryPullQPS, "registry-qps", c.RegistryPullQPS, "If > 0, limit registry pull QPS to this value.  If 0, unlimited.")
	fs.Int32Var(&c.RegistryBurst, "registry-burst", c.RegistryBurst, "Maximum size of a bursty pulls, temporarily allows pulls to burst to this number, while still not exceeding registry-qps. Only used if --registry-qps > 0")
	fs.Int32Var(&c.EventRecordQPS, "event-qps", c.EventRecordQPS, "QPS to limit event creations. The number must be >= 0. If 0 will use DefaultQPS: 5.")
	fs.Int32Var(&c.EventBurst, "event-burst", c.EventBurst, "Maximum size of a bursty event records, temporarily allows event records to burst to this number, while still not exceeding event-qps. The number must be >= 0. If 0 will use DefaultBurst: 10.")

	fs.BoolVar(&c.EnableDebuggingHandlers, "enable-debugging-handlers", c.EnableDebuggingHandlers, "Enables server endpoints for log collection and local running of containers and commands")
	fs.BoolVar(&c.EnableContentionProfiling, "contention-profiling", c.EnableContentionProfiling, "Enable lock contention profiling, if profiling is enabled")
	fs.Int32Var(&c.HealthzPort, "healthz-port", c.HealthzPort, "The port of the localhost healthz endpoint (set to 0 to disable)")
	fs.Var(utilflag.IPVar{Val: &c.HealthzBindAddress}, "healthz-bind-address", "The IP address for the healthz server to serve on (set to '0.0.0.0' or '::' for listening in all interfaces and IP families)")
	fs.Int32Var(&c.OOMScoreAdj, "oom-score-adj", c.OOMScoreAdj, "The oom-score-adj value for kubelet process. Values must be within the range [-1000, 1000]")
	fs.StringVar(&c.ClusterDomain, "cluster-domain", c.ClusterDomain, "Domain for this cluster.  If set, kubelet will configure all containers to search this domain in addition to the host's search domains")

	fs.StringVar(&c.VolumePluginDir, "volume-plugin-dir", c.VolumePluginDir, "The full path of the directory in which to search for additional third party volume plugins")
	fs.StringSliceVar(&c.ClusterDNS, "cluster-dns", c.ClusterDNS, "Comma-separated list of DNS server IP address.  This value is used for containers DNS server in case of Pods with \"dnsPolicy=ClusterFirst\". Note: all DNS servers appearing in the list MUST serve the same set of records otherwise name resolution within the cluster may not work correctly. There is no guarantee as to which DNS server may be contacted for name resolution.")
	fs.DurationVar(&c.StreamingConnectionIdleTimeout.Duration, "streaming-connection-idle-timeout", c.StreamingConnectionIdleTimeout.Duration, "Maximum time a streaming connection can be idle before the connection is automatically closed. 0 indicates no timeout. Example: '5m'")
	fs.DurationVar(&c.NodeStatusUpdateFrequency.Duration, "node-status-update-frequency", c.NodeStatusUpdateFrequency.Duration, "Specifies how often kubelet posts node status to master. Note: be cautious when changing the constant, it must work with nodeMonitorGracePeriod in nodecontroller.")
	fs.DurationVar(&c.ImageMinimumGCAge.Duration, "minimum-image-ttl-duration", c.ImageMinimumGCAge.Duration, "Minimum age for an unused image before it is garbage collected.  Examples: '300ms', '10s' or '2h45m'.")
	fs.Int32Var(&c.ImageGCHighThresholdPercent, "image-gc-high-threshold", c.ImageGCHighThresholdPercent, "The percent of disk usage after which image garbage collection is always run. Values must be within the range [0, 100], To disable image garbage collection, set to 100. ")
	fs.Int32Var(&c.ImageGCLowThresholdPercent, "image-gc-low-threshold", c.ImageGCLowThresholdPercent, "The percent of disk usage before which image garbage collection is never run. Lowest disk usage to garbage collect to. Values must be within the range [0, 100] and should not be larger than that of --image-gc-high-threshold.")
	fs.DurationVar(&c.VolumeStatsAggPeriod.Duration, "volume-stats-agg-period", c.VolumeStatsAggPeriod.Duration, "Specifies interval for kubelet to calculate and cache the volume disk usage for all pods and volumes.  To disable volume calculations, set to a negative number.")
	fs.Var(cliflag.NewMapStringBool(&c.FeatureGates), "feature-gates", "A set of key=value pairs that describe feature gates for alpha/experimental features.")
	fs.StringVar(&c.KubeletCgroups, "kubelet-cgroups", c.KubeletCgroups, "Optional absolute name of cgroups to create and run the Kubelet in.")
	fs.StringVar(&c.SystemCgroups, "system-cgroups", c.SystemCgroups, "Optional absolute name of cgroups in which to place all non-kernel processes that are not already inside a cgroup under '/'. Empty for no container. Rolling back the flag requires a reboot.")

	fs.StringVar(&c.ProviderID, "provider-id", c.ProviderID, "Unique identifier for identifying the node in a machine database, i.e cloudprovider")

	fs.BoolVar(&c.CgroupsPerQOS, "cgroups-per-qos", c.CgroupsPerQOS, "Enable creation of QoS cgroup hierarchy, if true top level QoS and pod cgroups are created.")
	fs.StringVar(&c.CgroupDriver, "cgroup-driver", c.CgroupDriver, "Driver that the kubelet uses to manipulate cgroups on the host.  Possible values: 'cgroupfs', 'systemd'")
	fs.StringVar(&c.CgroupRoot, "cgroup-root", c.CgroupRoot, "Optional root cgroup to use for pods. This is handled by the container runtime on a best effort basis. Default: '', which means use the container runtime default.")
	fs.StringVar(&c.CPUManagerPolicy, "cpu-manager-policy", c.CPUManagerPolicy, "CPU Manager policy to use. Possible values: 'none', 'static'.")
	fs.Var(cliflag.NewMapStringStringNoSplit(&c.CPUManagerPolicyOptions), "cpu-manager-policy-options", "A set of key=value CPU Manager policy options to use, to fine tune their behaviour. If not supplied, keep the default behaviour.")
	fs.DurationVar(&c.CPUManagerReconcilePeriod.Duration, "cpu-manager-reconcile-period", c.CPUManagerReconcilePeriod.Duration, "<Warning: Alpha feature> CPU Manager reconciliation period. Examples: '10s', or '1m'. If not supplied, defaults to 'NodeStatusUpdateFrequency'")
	fs.Var(cliflag.NewMapStringString(&c.QOSReserved), "qos-reserved", "<Warning: Alpha feature> A set of ResourceName=Percentage (e.g. memory=50%) pairs that describe how pod resource requests are reserved at the QoS level. Currently only memory is supported. Requires the QOSReserved feature gate to be enabled.")
	fs.StringVar(&c.TopologyManagerPolicy, "topology-manager-policy", c.TopologyManagerPolicy, "Topology Manager policy to use. Possible values: 'none', 'best-effort', 'restricted', 'single-numa-node'.")
	fs.DurationVar(&c.RuntimeRequestTimeout.Duration, "runtime-request-timeout", c.RuntimeRequestTimeout.Duration, "Timeout of all runtime requests except long running request - pull, logs, exec and attach. When timeout exceeded, kubelet will cancel the request, throw out an error and retry later.")
	fs.StringVar(&c.HairpinMode, "hairpin-mode", c.HairpinMode, "How should the kubelet setup hairpin NAT. This allows endpoints of a Service to loadbalance back to themselves if they should try to access their own Service. Valid values are \"promiscuous-bridge\", \"hairpin-veth\" and \"none\".")
	fs.Int32Var(&c.MaxPods, "max-pods", c.MaxPods, "Number of Pods that can run on this Kubelet.")

	fs.StringVar(&c.PodCIDR, "pod-cidr", c.PodCIDR, "The CIDR to use for pod IP addresses, only used in standalone mode.  In cluster mode, this is obtained from the master. For IPv6, the maximum number of IP's allocated is 65536")
	fs.Int64Var(&c.PodPidsLimit, "pod-max-pids", c.PodPidsLimit, "Set the maximum number of processes per pod.  If -1, the kubelet defaults to the node allocatable pid capacity.")

	fs.StringVar(&c.ResolverConfig, "resolv-conf", c.ResolverConfig, "Resolver configuration file used as the basis for the container DNS resolution configuration.")

	fs.BoolVar(&c.RunOnce, "runonce", c.RunOnce, "If true, exit after spawning pods from static pod files or remote urls. Exclusive with --enable-server")

	fs.BoolVar(&c.CPUCFSQuota, "cpu-cfs-quota", c.CPUCFSQuota, "Enable CPU CFS quota enforcement for containers that specify CPU limits")
	fs.DurationVar(&c.CPUCFSQuotaPeriod.Duration, "cpu-cfs-quota-period", c.CPUCFSQuotaPeriod.Duration, "Sets CPU CFS quota period value, cpu.cfs_period_us, defaults to Linux Kernel default")
	fs.BoolVar(&c.EnableControllerAttachDetach, "enable-controller-attach-detach", c.EnableControllerAttachDetach, "Enables the Attach/Detach controller to manage attachment/detachment of volumes scheduled to this node, and disables kubelet from executing any attach/detach operations")
	fs.BoolVar(&c.MakeIPTablesUtilChains, "make-iptables-util-chains", c.MakeIPTablesUtilChains, "If true, kubelet will ensure iptables utility rules are present on host.")
	fs.Int32Var(&c.IPTablesMasqueradeBit, "iptables-masquerade-bit", c.IPTablesMasqueradeBit, "The bit of the fwmark space to mark packets for SNAT. Must be within the range [0, 31]. Please match this parameter with corresponding parameter in kube-proxy.")
	fs.Int32Var(&c.IPTablesDropBit, "iptables-drop-bit", c.IPTablesDropBit, "The bit of the fwmark space to mark packets for dropping. Must be within the range [0, 31].")
	fs.StringVar(&c.ContainerLogMaxSize, "container-log-max-size", c.ContainerLogMaxSize, "<Warning: Beta feature> Set the maximum size (e.g. 10Mi) of container log file before it is rotated. This flag can only be used with --container-runtime=remote.")
	fs.Int32Var(&c.ContainerLogMaxFiles, "container-log-max-files", c.ContainerLogMaxFiles, "<Warning: Beta feature> Set the maximum number of container log files that can be present for a container. The number must be >= 2. This flag can only be used with --container-runtime=remote.")
	fs.StringSliceVar(&c.AllowedUnsafeSysctls, "allowed-unsafe-sysctls", c.AllowedUnsafeSysctls, "Comma-separated whitelist of unsafe sysctls or unsafe sysctl patterns (ending in *). Use these at your own risk.")

	fs.Int32Var(&c.NodeStatusMaxImages, "node-status-max-images", c.NodeStatusMaxImages, "The maximum number of images to report in Node.Status.Images. If -1 is specified, no cap will be applied.")
	fs.BoolVar(&c.KernelMemcgNotification, "kernel-memcg-notification", c.KernelMemcgNotification, "If enabled, the kubelet will integrate with the kernel memcg notification to determine if memory eviction thresholds are crossed rather than polling.")

	// Flags intended for testing, not recommended used in production environments.
	fs.Int64Var(&c.MaxOpenFiles, "max-open-files", c.MaxOpenFiles, "Number of files that can be opened by Kubelet process.")

	fs.StringVar(&c.ContentType, "kube-api-content-type", c.ContentType, "Content type of requests sent to apiserver.")
	fs.Int32Var(&c.KubeAPIQPS, "kube-api-qps", c.KubeAPIQPS, "QPS to use while talking with kubernetes apiserver. The number must be >= 0. If 0 will use DefaultQPS: 5. Doesn't cover events and node heartbeat apis which rate limiting is controlled by a different set of flags")
	fs.Int32Var(&c.KubeAPIBurst, "kube-api-burst", c.KubeAPIBurst, "Burst to use while talking with kubernetes apiserver. The number must be >= 0. If 0 will use DefaultBurst: 10. Doesn't cover events and node heartbeat apis which rate limiting is controlled by a different set of flags")
	fs.BoolVar(&c.SerializeImagePulls, "serialize-image-pulls", c.SerializeImagePulls, "Pull images one at a time. We recommend *not* changing the default value on nodes that run docker daemon with version < 1.9 or an Aufs storage backend. Issue #10959 has more details.")

	fs.Var(cliflag.NewLangleSeparatedMapStringString(&c.EvictionHard), "eviction-hard", "A set of eviction thresholds (e.g. memory.available<1Gi) that if met would trigger a pod eviction.")
	fs.Var(cliflag.NewLangleSeparatedMapStringString(&c.EvictionSoft), "eviction-soft", "A set of eviction thresholds (e.g. memory.available<1.5Gi) that if met over a corresponding grace period would trigger a pod eviction.")
	fs.Var(cliflag.NewMapStringString(&c.EvictionSoftGracePeriod), "eviction-soft-grace-period", "A set of eviction grace periods (e.g. memory.available=1m30s) that correspond to how long a soft eviction threshold must hold before triggering a pod eviction.")
	fs.DurationVar(&c.EvictionPressureTransitionPeriod.Duration, "eviction-pressure-transition-period", c.EvictionPressureTransitionPeriod.Duration, "Duration for which the kubelet has to wait before transitioning out of an eviction pressure condition.")
	fs.Int32Var(&c.EvictionMaxPodGracePeriod, "eviction-max-pod-grace-period", c.EvictionMaxPodGracePeriod, "Maximum allowed grace period (in seconds) to use when terminating pods in response to a soft eviction threshold being met.  If negative, defer to pod specified value.")
	fs.Var(cliflag.NewMapStringString(&c.EvictionMinimumReclaim), "eviction-minimum-reclaim", "A set of minimum reclaims (e.g. imagefs.available=2Gi) that describes the minimum amount of resource the kubelet will reclaim when performing a pod eviction if that resource is under pressure.")
	fs.Int32Var(&c.PodsPerCore, "pods-per-core", c.PodsPerCore, "Number of Pods per core that can run on this Kubelet. The total number of Pods on this Kubelet cannot exceed max-pods, so max-pods will be used if this calculation results in a larger number of Pods allowed on the Kubelet. A value of 0 disables this limit.")
	fs.BoolVar(&c.ProtectKernelDefaults, "protect-kernel-defaults", c.ProtectKernelDefaults, "Default kubelet behaviour for kernel tuning. If set, kubelet errors if any of kernel tunables is different than kubelet defaults.")
	fs.StringVar(&c.ReservedSystemCPUs, "reserved-cpus", c.ReservedSystemCPUs, "A comma-separated list of CPUs or CPU ranges that are reserved for system and kubernetes usage. This specific list will supersede cpu counts in --system-reserved and --kube-reserved.")
	fs.StringVar(&c.TopologyManagerScope, "topology-manager-scope", c.TopologyManagerScope, "Scope to which topology hints applied. Topology Manager collects hints from Hint Providers and applies them to defined scope to ensure the pod admission. Possible values: 'container', 'pod'.")
	// Node Allocatable Flags
	fs.Var(cliflag.NewMapStringString(&c.SystemReserved), "system-reserved", "A set of ResourceName=ResourceQuantity (e.g. cpu=200m,memory=500Mi,ephemeral-storage=1Gi) pairs that describe resources reserved for non-kubernetes components. Currently only cpu and memory are supported. See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/ for more detail. [default=none]")
	fs.Var(cliflag.NewMapStringString(&c.KubeReserved), "kube-reserved", "A set of ResourceName=ResourceQuantity (e.g. cpu=200m,memory=500Mi,ephemeral-storage=1Gi) pairs that describe resources reserved for kubernetes system components. Currently cpu, memory and local ephemeral storage for root file system are supported. See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/ for more detail. [default=none]")
	fs.StringSliceVar(&c.EnforceNodeAllocatable, "enforce-node-allocatable", c.EnforceNodeAllocatable, "A comma separated list of levels of node allocatable enforcement to be enforced by kubelet. Acceptable options are 'none', 'pods', 'system-reserved', and 'kube-reserved'. If the latter two options are specified, '--system-reserved-cgroup' and '--kube-reserved-cgroup' must also be set, respectively. If 'none' is specified, no additional options should be set. See https://kubernetes.io/docs/tasks/administer-cluster/reserve-compute-resources/ for more details.")
	fs.StringVar(&c.SystemReservedCgroup, "system-reserved-cgroup", c.SystemReservedCgroup, "Absolute name of the top level cgroup that is used to manage non-kubernetes components for which compute resources were reserved via '--system-reserved' flag. Ex. '/system-reserved'. [default='']")
	fs.StringVar(&c.KubeReservedCgroup, "kube-reserved-cgroup", c.KubeReservedCgroup, "Absolute name of the top level cgroup that is used to manage kubernetes components for which compute resources were reserved via '--kube-reserved' flag. Ex. '/kube-reserved'. [default='']")

	// Graduated experimental flags, kept for backward compatibility
	fs.BoolVar(&c.KernelMemcgNotification, "experimental-kernel-memcg-notification", c.KernelMemcgNotification, "Use kernelMemcgNotification configuration, this flag will be removed in 1.23.")

	// Memory Manager Flags
	fs.StringVar(&c.MemoryManagerPolicy, "memory-manager-policy", c.MemoryManagerPolicy, "Memory Manager policy to use. Possible values: 'None', 'Static'.")
	fs.Var(&utilflag.ReservedMemoryVar{Value: &c.ReservedMemory}, "reserved-memory", "A comma separated list of memory reservations for NUMA nodes. (e.g. --reserved-memory 0:memory=1Gi,hugepages-1M=2Gi --reserved-memory 1:memory=2Gi). The total sum for each memory type should be equal to the sum of kube-reserved, system-reserved and eviction-threshold. See https://kubernetes.io/docs/tasks/administer-cluster/memory-manager/#reserved-memory-flag for more details.")
}

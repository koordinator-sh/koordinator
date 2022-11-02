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

package kubelet

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
)

var (
	kubeletArgsWithoutCPUManagerPolicy                   = []string{"/usr/bin/kubelet", "--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf", "--kubeconfig=/etc/kubernetes/kubelet.conf", "--container-log-max-files", "10", "--container-log-max-size=100Mi", "--max-pods", "213", "--pod-max-pids", "16384", "--pod-manifest-path=/etc/kubernetes/manifests", "--network-plugin=cni", "--cni-conf-dir=/etc/cni/net.d", "--cni-bin-dir=/opt/cni/bin", "--v=3", "--enable-controller-attach-detach=true", "--cluster-dns=192.168.0.10", "--pod-infra-container-image=registry-vpc.cn-hangzhou.aliyuncs.com/acs/pause:3.5", "--enable-load-reader", "--cluster-domain=cluster.local", "--cloud-provider=external", "--hostname-override=cn-hangzhou.10.0.4.18", "--provider-id=cn-hangzhou.i-bp1049apy5ggvw0qbuh6", "--authorization-mode=Webhook", "--authentication-token-webhook=true", "--anonymous-auth=false", "--client-ca-file=/etc/kubernetes/pki/ca.crt", "--cgroup-driver=systemd", "--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_128_GCM_SHA256", "--tls-cert-file=/var/lib/kubelet/pki/kubelet.crt", "--tls-private-key-file=/var/lib/kubelet/pki/kubelet.key", "--rotate-certificates=true", "--cert-dir=/var/lib/kubelet/pki", "--node-labels=alibabacloud.com/nodepool-id=npab2a7b3f6ce84f5aacc55b08df6b8ecd,ack.aliyun.com=c5558876cbc06429782797388d4abe3e0", "--eviction-hard=imagefs.available<15%,memory.available<300Mi,nodefs.available<10%,nodefs.inodesFree<5%", "--system-reserved=cpu=200m,memory=2732Mi", "--kube-reserved=cpu=1800m,memory=2732Mi", "--kube-reserved=pid=1000", "--system-reserved=pid=1000", "--container-runtime=remote", "--container-runtime-endpoint=/var/run/containerd/containerd.sock"}
	kubeletArgsWithNoneCPUManagerPolicy                  = []string{"/usr/bin/kubelet", "--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf", "--kubeconfig=/etc/kubernetes/kubelet.conf", "--container-log-max-files", "10", "--container-log-max-size=100Mi", "--max-pods", "213", "--pod-max-pids", "16384", "--pod-manifest-path=/etc/kubernetes/manifests", "--network-plugin=cni", "--cni-conf-dir=/etc/cni/net.d", "--cni-bin-dir=/opt/cni/bin", "--v=3", "--enable-controller-attach-detach=true", "--cluster-dns=192.168.0.10", "--pod-infra-container-image=registry-vpc.cn-hangzhou.aliyuncs.com/acs/pause:3.5", "--enable-load-reader", "--cluster-domain=cluster.local", "--cloud-provider=external", "--hostname-override=cn-hangzhou.10.0.4.18", "--provider-id=cn-hangzhou.i-bp1049apy5ggvw0qbuh6", "--authorization-mode=Webhook", "--authentication-token-webhook=true", "--anonymous-auth=false", "--client-ca-file=/etc/kubernetes/pki/ca.crt", "--cgroup-driver=systemd", "--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_128_GCM_SHA256", "--tls-cert-file=/var/lib/kubelet/pki/kubelet.crt", "--tls-private-key-file=/var/lib/kubelet/pki/kubelet.key", "--rotate-certificates=true", "--cert-dir=/var/lib/kubelet/pki", "--node-labels=alibabacloud.com/nodepool-id=npab2a7b3f6ce84f5aacc55b08df6b8ecd,ack.aliyun.com=c5558876cbc06429782797388d4abe3e0", "--eviction-hard=imagefs.available<15%,memory.available<300Mi,nodefs.available<10%,nodefs.inodesFree<5%", "--system-reserved=cpu=200m,memory=2732Mi", "--kube-reserved=cpu=1800m,memory=2732Mi", "--kube-reserved=pid=1000", "--system-reserved=pid=1000", "--cpu-manager-policy=none", "--container-runtime=remote", "--container-runtime-endpoint=/var/run/containerd/containerd.sock"}
	kubeletArgsWithStaticCPUManagerPolicy                = []string{"/usr/bin/kubelet", "--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf", "--kubeconfig=/etc/kubernetes/kubelet.conf", "--container-log-max-files", "10", "--container-log-max-size=100Mi", "--max-pods", "213", "--pod-max-pids", "16384", "--pod-manifest-path=/etc/kubernetes/manifests", "--network-plugin=cni", "--cni-conf-dir=/etc/cni/net.d", "--cni-bin-dir=/opt/cni/bin", "--v=3", "--enable-controller-attach-detach=true", "--cluster-dns=192.168.0.10", "--pod-infra-container-image=registry-vpc.cn-hangzhou.aliyuncs.com/acs/pause:3.5", "--enable-load-reader", "--cluster-domain=cluster.local", "--cloud-provider=external", "--hostname-override=cn-hangzhou.10.0.4.18", "--provider-id=cn-hangzhou.i-bp1049apy5ggvw0qbuh6", "--authorization-mode=Webhook", "--authentication-token-webhook=true", "--anonymous-auth=false", "--client-ca-file=/etc/kubernetes/pki/ca.crt", "--cgroup-driver=systemd", "--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_128_GCM_SHA256", "--tls-cert-file=/var/lib/kubelet/pki/kubelet.crt", "--tls-private-key-file=/var/lib/kubelet/pki/kubelet.key", "--rotate-certificates=true", "--cert-dir=/var/lib/kubelet/pki", "--node-labels=alibabacloud.com/nodepool-id=npab2a7b3f6ce84f5aacc55b08df6b8ecd,ack.aliyun.com=c5558876cbc06429782797388d4abe3e0", "--eviction-hard=imagefs.available<15%,memory.available<300Mi,nodefs.available<10%,nodefs.inodesFree<5%", "--system-reserved=cpu=200m,memory=2732Mi", "--kube-reserved=cpu=1800m,memory=2732Mi", "--kube-reserved=pid=1000", "--system-reserved=pid=1000", "--cpu-manager-policy=static", "--container-runtime=remote", "--container-runtime-endpoint=/var/run/containerd/containerd.sock"}
	kubeletArgsWithStaticCPUManagerPolicyAndReservedCPUs = []string{"/usr/bin/kubelet", "--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf", "--kubeconfig=/etc/kubernetes/kubelet.conf", "--container-log-max-files", "10", "--container-log-max-size=100Mi", "--max-pods", "213", "--pod-max-pids", "16384", "--pod-manifest-path=/etc/kubernetes/manifests", "--network-plugin=cni", "--cni-conf-dir=/etc/cni/net.d", "--cni-bin-dir=/opt/cni/bin", "--v=3", "--enable-controller-attach-detach=true", "--cluster-dns=192.168.0.10", "--pod-infra-container-image=registry-vpc.cn-hangzhou.aliyuncs.com/acs/pause:3.5", "--enable-load-reader", "--cluster-domain=cluster.local", "--cloud-provider=external", "--hostname-override=cn-hangzhou.10.0.4.18", "--provider-id=cn-hangzhou.i-bp1049apy5ggvw0qbuh6", "--authorization-mode=Webhook", "--authentication-token-webhook=true", "--anonymous-auth=false", "--client-ca-file=/etc/kubernetes/pki/ca.crt", "--cgroup-driver=systemd", "--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_128_GCM_SHA256", "--tls-cert-file=/var/lib/kubelet/pki/kubelet.crt", "--tls-private-key-file=/var/lib/kubelet/pki/kubelet.key", "--rotate-certificates=true", "--cert-dir=/var/lib/kubelet/pki", "--node-labels=alibabacloud.com/nodepool-id=npab2a7b3f6ce84f5aacc55b08df6b8ecd,ack.aliyun.com=c5558876cbc06429782797388d4abe3e0", "--eviction-hard=imagefs.available<15%,memory.available<300Mi,nodefs.available<10%,nodefs.inodesFree<5%", "--system-reserved=cpu=200m,memory=2732Mi", "--kube-reserved=cpu=1800m,memory=2732Mi", "--kube-reserved=pid=1000", "--system-reserved=pid=1000", "--cpu-manager-policy=static", "--container-runtime=remote", "--container-runtime-endpoint=/var/run/containerd/containerd.sock", "--reserved-cpus=1,2,3,4"}
)

func TestGetStaticCPUManagerPolicyReservedCPUs(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantReservedCPUs string
	}{
		{
			name:             "none policy",
			args:             kubeletArgsWithNoneCPUManagerPolicy,
			wantReservedCPUs: "",
		},
		{
			name:             "static policy",
			args:             kubeletArgsWithStaticCPUManagerPolicy,
			wantReservedCPUs: "0,6",
		},
		{
			name:             "static policy with specified reserved cpus",
			args:             kubeletArgsWithStaticCPUManagerPolicyAndReservedCPUs,
			wantReservedCPUs: "1-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := NewKubeletOptions(tt.args)
			assert.NoError(t, err)
			assert.NotNil(t, options)

			reservedCPUS, err := GetStaticCPUManagerPolicyReservedCPUs(topoDualSocketHT, options)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantReservedCPUs, reservedCPUS.String())
		})
	}
}

func TestKubeletConfigWithFile(t *testing.T) {
	staticPolicyFile := `kind: KubeletConfiguration
apiVersion: kubelet.config.k8s.io/v1beta1
cpuManagerPolicy: static`
	tests := []struct {
		name          string
		args          []string
		configContent string
		wantPolicy    string
	}{
		{
			name:          "use file config",
			args:          kubeletArgsWithoutCPUManagerPolicy,
			configContent: staticPolicyFile,
			wantPolicy:    string(cpumanager.PolicyStatic),
		},
		{
			name:          "override config file by flags",
			args:          kubeletArgsWithNoneCPUManagerPolicy,
			configContent: staticPolicyFile,
			wantPolicy:    string(cpumanager.PolicyNone),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempFile, err := os.CreateTemp("", "koordlet-ut-")
			assert.NoError(t, err)
			assert.NotNil(t, tempFile)
			fileName := tempFile.Name()
			defer func() {
				tempFile.Close()
				os.Remove(fileName)
			}()

			_, err = tempFile.WriteString(tt.configContent)
			assert.NoError(t, err)

			args := make([]string, len(tt.args))
			copy(args, tt.args)
			args = append(args, fmt.Sprintf("--config=%s", fileName))

			options, err := NewKubeletOptions(args)
			assert.NoError(t, err)
			assert.NotNil(t, options)
			assert.Equal(t, tt.wantPolicy, options.CPUManagerPolicy)
		})
	}
}

func TestKubeletLogFlags(t *testing.T) {
	kubeletArgs := make([]string, len(kubeletArgsWithNoneCPUManagerPolicy))
	copy(kubeletArgs, kubeletArgsWithNoneCPUManagerPolicy)
	kubeletArgs = append(kubeletArgs, "--v=666")

	assert.False(t, klog.V(666).Enabled())
	_, err := NewKubeletOptions(kubeletArgs)
	assert.NoError(t, err)
	assert.False(t, klog.V(666).Enabled())
}

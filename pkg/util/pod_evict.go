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

package util

import (
	"context"
	"fmt"
	"strings"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	EvictionKind            = "Eviction"
	EvictionGroupName       = "policy"
	EvictionSubResourceName = "pods/eviction"
)

// EvictPodByVersion evicts Pods using the policy/v1 Eviction API (k8s >= 1.22).
// The v1beta1 eviction API was removed in k8s 1.25 and is no longer supported.
func EvictPodByVersion(ctx context.Context, kubernetes kubernetes.Interface, namespace, name string, opts metav1.DeleteOptions, evictVersion string) error {
	if evictVersion == "v1" {
		return kubernetes.CoreV1().Pods(namespace).EvictV1(ctx, &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			DeleteOptions: &opts,
		})
	}

	return fmt.Errorf("not support evict version, %s", evictVersion)
}

func FindSupportedEvictVersion(client kubernetes.Interface) (version string, err error) {
	var (
		groupVersion string
	)
	groupVersion, err = SupportEviction(client)
	if err != nil {
		return
	}
	if groupVersion == "" || !strings.Contains(groupVersion, "/") {
		return
	}
	version = strings.Split(groupVersion, "/")[1]
	return
}

func SupportEviction(client kubernetes.Interface) (string, error) {
	var (
		serverGroups          *metav1.APIGroupList
		resourceList          *metav1.APIResourceList
		foundPolicyGroup      bool
		preferredGroupVersion string
		groupVersion          string
		err                   error
	)
	discoveryClient := client.Discovery()
	serverGroups, err = discoveryClient.ServerGroups()
	if serverGroups == nil || err != nil {
		return groupVersion, err
	}

	for _, serverGroup := range serverGroups.Groups {
		if serverGroup.Name == EvictionGroupName {
			foundPolicyGroup = true
			preferredGroupVersion = serverGroup.PreferredVersion.GroupVersion
			break
		}
	}
	if !foundPolicyGroup {
		return groupVersion, err
	}

	resourceList, err = discoveryClient.ServerResourcesForGroupVersion("v1")
	if err != nil {
		if errors.IsNotFound(err) {
			return groupVersion, nil
		}
		return groupVersion, err
	}
	for _, resource := range resourceList.APIResources {
		if resource.Name == EvictionSubResourceName && resource.Kind == EvictionKind {
			groupVersion = resource.Group + "/" + resource.Version
			return groupVersion, err
		}
	}
	groupVersion = preferredGroupVersion
	return groupVersion, err
}

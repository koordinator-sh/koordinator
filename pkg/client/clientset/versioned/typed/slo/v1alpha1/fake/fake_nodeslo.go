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
// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeNodeSLOs implements NodeSLOInterface
type FakeNodeSLOs struct {
	Fake *FakeSloV1alpha1
}

var nodeslosResource = schema.GroupVersionResource{Group: "slo.koordinator.sh", Version: "v1alpha1", Resource: "nodeslos"}

var nodeslosKind = schema.GroupVersionKind{Group: "slo.koordinator.sh", Version: "v1alpha1", Kind: "NodeSLO"}

// Get takes name of the nodeSLO, and returns the corresponding nodeSLO object, and an error if there is any.
func (c *FakeNodeSLOs) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.NodeSLO, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(nodeslosResource, name), &v1alpha1.NodeSLO{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.NodeSLO), err
}

// List takes label and field selectors, and returns the list of NodeSLOs that match those selectors.
func (c *FakeNodeSLOs) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.NodeSLOList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(nodeslosResource, nodeslosKind, opts), &v1alpha1.NodeSLOList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.NodeSLOList{ListMeta: obj.(*v1alpha1.NodeSLOList).ListMeta}
	for _, item := range obj.(*v1alpha1.NodeSLOList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested nodeSLOs.
func (c *FakeNodeSLOs) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(nodeslosResource, opts))
}

// Create takes the representation of a nodeSLO and creates it.  Returns the server's representation of the nodeSLO, and an error, if there is any.
func (c *FakeNodeSLOs) Create(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.CreateOptions) (result *v1alpha1.NodeSLO, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(nodeslosResource, nodeSLO), &v1alpha1.NodeSLO{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.NodeSLO), err
}

// Update takes the representation of a nodeSLO and updates it. Returns the server's representation of the nodeSLO, and an error, if there is any.
func (c *FakeNodeSLOs) Update(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.UpdateOptions) (result *v1alpha1.NodeSLO, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(nodeslosResource, nodeSLO), &v1alpha1.NodeSLO{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.NodeSLO), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeNodeSLOs) UpdateStatus(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.UpdateOptions) (*v1alpha1.NodeSLO, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(nodeslosResource, "status", nodeSLO), &v1alpha1.NodeSLO{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.NodeSLO), err
}

// Delete takes name of the nodeSLO and deletes it. Returns an error if one occurs.
func (c *FakeNodeSLOs) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteAction(nodeslosResource, name), &v1alpha1.NodeSLO{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeNodeSLOs) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(nodeslosResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.NodeSLOList{})
	return err
}

// Patch applies the patch and returns the patched nodeSLO.
func (c *FakeNodeSLOs) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.NodeSLO, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(nodeslosResource, name, pt, data, subresources...), &v1alpha1.NodeSLO{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.NodeSLO), err
}

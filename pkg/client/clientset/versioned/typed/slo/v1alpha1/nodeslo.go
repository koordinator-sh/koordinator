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

package v1alpha1

import (
	"context"
	"time"

	v1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	scheme "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// NodeSLOsGetter has a method to return a NodeSLOInterface.
// A group's client should implement this interface.
type NodeSLOsGetter interface {
	NodeSLOs() NodeSLOInterface
}

// NodeSLOInterface has methods to work with NodeSLO resources.
type NodeSLOInterface interface {
	Create(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.CreateOptions) (*v1alpha1.NodeSLO, error)
	Update(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.UpdateOptions) (*v1alpha1.NodeSLO, error)
	UpdateStatus(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.UpdateOptions) (*v1alpha1.NodeSLO, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha1.NodeSLO, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha1.NodeSLOList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.NodeSLO, err error)
	NodeSLOExpansion
}

// nodeSLOs implements NodeSLOInterface
type nodeSLOs struct {
	client rest.Interface
}

// newNodeSLOs returns a NodeSLOs
func newNodeSLOs(c *SloV1alpha1Client) *nodeSLOs {
	return &nodeSLOs{
		client: c.RESTClient(),
	}
}

// Get takes name of the nodeSLO, and returns the corresponding nodeSLO object, and an error if there is any.
func (c *nodeSLOs) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.NodeSLO, err error) {
	result = &v1alpha1.NodeSLO{}
	err = c.client.Get().
		Resource("nodeslos").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of NodeSLOs that match those selectors.
func (c *nodeSLOs) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.NodeSLOList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha1.NodeSLOList{}
	err = c.client.Get().
		Resource("nodeslos").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested nodeSLOs.
func (c *nodeSLOs) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Resource("nodeslos").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a nodeSLO and creates it.  Returns the server's representation of the nodeSLO, and an error, if there is any.
func (c *nodeSLOs) Create(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.CreateOptions) (result *v1alpha1.NodeSLO, err error) {
	result = &v1alpha1.NodeSLO{}
	err = c.client.Post().
		Resource("nodeslos").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(nodeSLO).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a nodeSLO and updates it. Returns the server's representation of the nodeSLO, and an error, if there is any.
func (c *nodeSLOs) Update(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.UpdateOptions) (result *v1alpha1.NodeSLO, err error) {
	result = &v1alpha1.NodeSLO{}
	err = c.client.Put().
		Resource("nodeslos").
		Name(nodeSLO.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(nodeSLO).
		Do(ctx).
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *nodeSLOs) UpdateStatus(ctx context.Context, nodeSLO *v1alpha1.NodeSLO, opts v1.UpdateOptions) (result *v1alpha1.NodeSLO, err error) {
	result = &v1alpha1.NodeSLO{}
	err = c.client.Put().
		Resource("nodeslos").
		Name(nodeSLO.Name).
		SubResource("status").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(nodeSLO).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the nodeSLO and deletes it. Returns an error if one occurs.
func (c *nodeSLOs) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Resource("nodeslos").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *nodeSLOs) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Resource("nodeslos").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched nodeSLO.
func (c *nodeSLOs) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.NodeSLO, err error) {
	result = &v1alpha1.NodeSLO{}
	err = c.client.Patch(pt).
		Resource("nodeslos").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}

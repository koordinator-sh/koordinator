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

	v1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	scheme "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// ElasticQuotaProfilesGetter has a method to return a ElasticQuotaProfileInterface.
// A group's client should implement this interface.
type ElasticQuotaProfilesGetter interface {
	ElasticQuotaProfiles(namespace string) ElasticQuotaProfileInterface
}

// ElasticQuotaProfileInterface has methods to work with ElasticQuotaProfile resources.
type ElasticQuotaProfileInterface interface {
	Create(ctx context.Context, elasticQuotaProfile *v1alpha1.ElasticQuotaProfile, opts v1.CreateOptions) (*v1alpha1.ElasticQuotaProfile, error)
	Update(ctx context.Context, elasticQuotaProfile *v1alpha1.ElasticQuotaProfile, opts v1.UpdateOptions) (*v1alpha1.ElasticQuotaProfile, error)
	UpdateStatus(ctx context.Context, elasticQuotaProfile *v1alpha1.ElasticQuotaProfile, opts v1.UpdateOptions) (*v1alpha1.ElasticQuotaProfile, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha1.ElasticQuotaProfile, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha1.ElasticQuotaProfileList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.ElasticQuotaProfile, err error)
	ElasticQuotaProfileExpansion
}

// elasticQuotaProfiles implements ElasticQuotaProfileInterface
type elasticQuotaProfiles struct {
	client rest.Interface
	ns     string
}

// newElasticQuotaProfiles returns a ElasticQuotaProfiles
func newElasticQuotaProfiles(c *QuotaV1alpha1Client, namespace string) *elasticQuotaProfiles {
	return &elasticQuotaProfiles{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the elasticQuotaProfile, and returns the corresponding elasticQuotaProfile object, and an error if there is any.
func (c *elasticQuotaProfiles) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.ElasticQuotaProfile, err error) {
	result = &v1alpha1.ElasticQuotaProfile{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of ElasticQuotaProfiles that match those selectors.
func (c *elasticQuotaProfiles) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.ElasticQuotaProfileList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha1.ElasticQuotaProfileList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested elasticQuotaProfiles.
func (c *elasticQuotaProfiles) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a elasticQuotaProfile and creates it.  Returns the server's representation of the elasticQuotaProfile, and an error, if there is any.
func (c *elasticQuotaProfiles) Create(ctx context.Context, elasticQuotaProfile *v1alpha1.ElasticQuotaProfile, opts v1.CreateOptions) (result *v1alpha1.ElasticQuotaProfile, err error) {
	result = &v1alpha1.ElasticQuotaProfile{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(elasticQuotaProfile).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a elasticQuotaProfile and updates it. Returns the server's representation of the elasticQuotaProfile, and an error, if there is any.
func (c *elasticQuotaProfiles) Update(ctx context.Context, elasticQuotaProfile *v1alpha1.ElasticQuotaProfile, opts v1.UpdateOptions) (result *v1alpha1.ElasticQuotaProfile, err error) {
	result = &v1alpha1.ElasticQuotaProfile{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		Name(elasticQuotaProfile.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(elasticQuotaProfile).
		Do(ctx).
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *elasticQuotaProfiles) UpdateStatus(ctx context.Context, elasticQuotaProfile *v1alpha1.ElasticQuotaProfile, opts v1.UpdateOptions) (result *v1alpha1.ElasticQuotaProfile, err error) {
	result = &v1alpha1.ElasticQuotaProfile{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		Name(elasticQuotaProfile.Name).
		SubResource("status").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(elasticQuotaProfile).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the elasticQuotaProfile and deletes it. Returns an error if one occurs.
func (c *elasticQuotaProfiles) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *elasticQuotaProfiles) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched elasticQuotaProfile.
func (c *elasticQuotaProfiles) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.ElasticQuotaProfile, err error) {
	result = &v1alpha1.ElasticQuotaProfile{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("elasticquotaprofiles").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}

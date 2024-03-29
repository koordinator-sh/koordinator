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

// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// ElasticQuotaProfileLister helps list ElasticQuotaProfiles.
// All objects returned here must be treated as read-only.
type ElasticQuotaProfileLister interface {
	// List lists all ElasticQuotaProfiles in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.ElasticQuotaProfile, err error)
	// ElasticQuotaProfiles returns an object that can list and get ElasticQuotaProfiles.
	ElasticQuotaProfiles(namespace string) ElasticQuotaProfileNamespaceLister
	ElasticQuotaProfileListerExpansion
}

// elasticQuotaProfileLister implements the ElasticQuotaProfileLister interface.
type elasticQuotaProfileLister struct {
	indexer cache.Indexer
}

// NewElasticQuotaProfileLister returns a new ElasticQuotaProfileLister.
func NewElasticQuotaProfileLister(indexer cache.Indexer) ElasticQuotaProfileLister {
	return &elasticQuotaProfileLister{indexer: indexer}
}

// List lists all ElasticQuotaProfiles in the indexer.
func (s *elasticQuotaProfileLister) List(selector labels.Selector) (ret []*v1alpha1.ElasticQuotaProfile, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.ElasticQuotaProfile))
	})
	return ret, err
}

// ElasticQuotaProfiles returns an object that can list and get ElasticQuotaProfiles.
func (s *elasticQuotaProfileLister) ElasticQuotaProfiles(namespace string) ElasticQuotaProfileNamespaceLister {
	return elasticQuotaProfileNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ElasticQuotaProfileNamespaceLister helps list and get ElasticQuotaProfiles.
// All objects returned here must be treated as read-only.
type ElasticQuotaProfileNamespaceLister interface {
	// List lists all ElasticQuotaProfiles in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.ElasticQuotaProfile, err error)
	// Get retrieves the ElasticQuotaProfile from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1alpha1.ElasticQuotaProfile, error)
	ElasticQuotaProfileNamespaceListerExpansion
}

// elasticQuotaProfileNamespaceLister implements the ElasticQuotaProfileNamespaceLister
// interface.
type elasticQuotaProfileNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ElasticQuotaProfiles in the indexer for a given namespace.
func (s elasticQuotaProfileNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.ElasticQuotaProfile, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.ElasticQuotaProfile))
	})
	return ret, err
}

// Get retrieves the ElasticQuotaProfile from the indexer for a given namespace and name.
func (s elasticQuotaProfileNamespaceLister) Get(name string) (*v1alpha1.ElasticQuotaProfile, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("elasticquotaprofile"), name)
	}
	return obj.(*v1alpha1.ElasticQuotaProfile), nil
}

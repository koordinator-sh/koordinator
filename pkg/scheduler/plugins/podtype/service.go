package podtype

import (
	"k8s.io/klog/v2"
)

// Service provides service layer functionality for the podtype plugin
type Service struct {
	cache *PodTypeCache
}

// NewServiceFromCache receives existing cache from plugin
func NewServiceFromCache(cache *PodTypeCache) *Service {
	return &Service{cache: cache}
}

// GetCache returns the pod type cache
func (s *Service) GetCache() *PodTypeCache {
	return s.cache
}

// Start starts the service
func (s *Service) Start() error {
	klog.V(4).InfoS("Started podtype service")
	return nil
}

// Stop stops the service
func (s *Service) Stop() {
	klog.V(4).InfoS("Stopped podtype service")
}

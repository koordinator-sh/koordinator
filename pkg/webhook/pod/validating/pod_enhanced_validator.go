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

package validating

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/features"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const (
	ConfigKeyEnable = "enable"
	ConfigKeyRules  = "rules"

	ClientNamePodEnhancedValidator = "pod-enhanced-validator"
)

var (
	// PodEnhancedValidatorConfigNamespace defines the namespace for the PodEnhancedValidator configuration.
	PodEnhancedValidatorConfigNamespace = "koordinator-system"
	// PodEnhancedValidatorConfigName defines the name for the PodEnhancedValidator configuration.
	PodEnhancedValidatorConfigName = "pod-enhanced-validator-config"
)

func (h *PodValidatingHandler) podEnhancedValidate(ctx context.Context, req admission.Request) (string, error) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.EnablePodEnhancedValidator) {
		return "", nil
	}

	newPod := &corev1.Pod{}
	switch req.Operation {
	case admissionv1.Create, admissionv1.Update:
		if err := h.Decoder.DecodeRaw(req.Object, newPod); err != nil {
			return "", err
		}
	default:
		return "", nil
	}

	reason, err := h.PodEnhancedValidator.ValidatePod(newPod)
	if err != nil {
		return reason, err
	}
	return "", nil
}

func InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&PodEnhancedValidatorConfigNamespace, "pod-enhanced-validator-config-namespace",
		PodEnhancedValidatorConfigNamespace, "The namespace for the pod-enhanced-validator configuration.")
	fs.StringVar(&PodEnhancedValidatorConfigName, "pod-enhanced-validator-config-name",
		PodEnhancedValidatorConfigName, "The name for the pod-enhanced-validator configuration.")
}

// ValidationRule defines a single validation rule
type ValidationRule struct {
	// Name is the unique identifier for this rule
	Name string `json:"name"`
	// RequiredLabels specifies label keys that must be present on pods
	RequiredLabels []string `json:"requiredLabels,omitempty"`
	// NamespaceWhitelist contains namespaces that are exempt from this validation rule
	NamespaceWhitelist []string `json:"namespaceWhitelist,omitempty"`

	// namespaceWhitelistSet is a cached set for fast namespace lookup
	// this field is not serialized and is built from NamespaceWhitelist
	namespaceWhitelistSet sets.Set[string] `json:"-"`
}

// isNamespaceWhitelisted checks if a namespace is in the whitelist using the cached map
func (r *ValidationRule) isNamespaceWhitelisted(namespace string) bool {
	if r.namespaceWhitelistSet == nil {
		return false
	}
	return r.namespaceWhitelistSet.Has(namespace)
}

// PodEnhancedValidatorConfig defines the configuration for pod enhanced validation
type PodEnhancedValidatorConfig struct {
	// Enable controls whether pod enhanced validation is enabled
	Enable bool `json:"enable"`
	// Rules contains the list of validation rules to apply
	Rules []ValidationRule `json:"rules,omitempty"`
}

// PodEnhancedValidator manages the pod-enhanced-validator configuration with hot reload support
type PodEnhancedValidator struct {
	kubeClient      kubernetes.Interface
	config          *PodEnhancedValidatorConfig
	configName      string
	configNamespace string

	sync.RWMutex
}

// NewPodEnhancedValidator creates a new config manager
func NewPodEnhancedValidator(kubeClient kubernetes.Interface) *PodEnhancedValidator {
	return &PodEnhancedValidator{
		kubeClient:      kubeClient,
		configName:      PodEnhancedValidatorConfigName,
		configNamespace: PodEnhancedValidatorConfigNamespace,
		config: &PodEnhancedValidatorConfig{
			Enable: false,
			Rules:  []ValidationRule{},
		},
	}
}

// Start initializes and starts the informer for hot reload
func (m *PodEnhancedValidator) Start(ctx context.Context) error {
	// create informer for hot reload
	factory := informers.NewSharedInformerFactoryWithOptions(
		m.kubeClient,
		time.Minute*5,
		informers.WithNamespace(m.configNamespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", m.configName).String()
		}),
	)
	informer := factory.Core().V1().ConfigMaps().Informer()
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == m.configName {
				m.handleConfigMapUpdate(cm)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if cm, ok := newObj.(*corev1.ConfigMap); ok && cm.Name == m.configName {
				m.handleConfigMapUpdate(cm)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == m.configName {
				m.handleConfigMapDelete()
			}
		},
	}

	_, err := frameworkexthelper.ForceSyncFromInformer(ctx.Done(), factory, informer, handler)
	if err != nil {
		return fmt.Errorf("failed to initialize informer, err=%w", err)
	}

	klog.Info("pod-enhanced-validator started successfully")
	return nil
}

// handleConfigMapUpdate handles configmap update events
func (m *PodEnhancedValidator) handleConfigMapUpdate(cm *corev1.ConfigMap) {
	config, err := m.parseConfig(cm)
	if err != nil {
		klog.Errorf("failed to parse config for pod-enhanced-validator, err=%v", err)
		return
	}

	m.Lock()
	defer m.Unlock()

	// check if the config has changed
	if reflect.DeepEqual(m.config, config) {
		klog.V(4).Infof("skip updating unchanged config for pod-enhanced-validator")
		return
	}

	m.config = config

	klog.Infof("updated pod-enhanced-validator config with %d rules: enable=%v",
		len(config.Rules), config.Enable)
}

// handleConfigMapDelete handles configmap delete events
func (m *PodEnhancedValidator) handleConfigMapDelete() {
	m.Lock()
	defer m.Unlock()

	// reset to default config
	m.config = &PodEnhancedValidatorConfig{
		Enable: false,
		Rules:  []ValidationRule{},
	}
	klog.Info("pod-enhanced-validator configmap deleted, reset to default config")
}

// parseConfig parses configmap data
func (m *PodEnhancedValidator) parseConfig(cm *corev1.ConfigMap) (*PodEnhancedValidatorConfig, error) {
	var config PodEnhancedValidatorConfig

	// parse enable field with case-insensitive comparison
	if enableData, ok := cm.Data[ConfigKeyEnable]; ok {
		enableData = strings.ToLower(strings.TrimSpace(enableData))
		config.Enable = enableData == "true"
	}

	// parse rules field
	if rulesData, ok := cm.Data[ConfigKeyRules]; ok {
		var rules []ValidationRule
		if err := json.Unmarshal([]byte(rulesData), &rules); err != nil {
			return nil, fmt.Errorf("failed to unmarshal validation rules: %w", err)
		}
		config.Rules = rules
	} else {
		config.Rules = []ValidationRule{} // default to empty rules if not specified
	}

	// build namespace whitelist sets for all validation rules
	m.buildNamespaceWhitelistSet(&config)

	return &config, nil
}

// buildNamespaceWhitelistSet builds namespace whitelist sets for all validation rules
func (m *PodEnhancedValidator) buildNamespaceWhitelistSet(config *PodEnhancedValidatorConfig) {
	for i := range config.Rules {
		rule := &config.Rules[i]

		// Build namespace whitelist set
		if len(rule.NamespaceWhitelist) > 0 {
			rule.namespaceWhitelistSet = sets.New[string]()
			for _, ns := range rule.NamespaceWhitelist {
				if ns != "" {
					rule.namespaceWhitelistSet.Insert(ns)
				}
			}
		}
	}
}

// GetConfig returns the current configuration
func (m *PodEnhancedValidator) GetConfig() *PodEnhancedValidatorConfig {
	m.RLock()
	defer m.RUnlock()
	return m.config
}

// ValidatePod validates a pod against all configured rules
func (m *PodEnhancedValidator) ValidatePod(pod *corev1.Pod) (string, error) {
	config := m.GetConfig()
	if !config.Enable {
		return "", nil
	}
	for _, rule := range config.Rules {
		// check if namespace is whitelisted for this rule
		if rule.isNamespaceWhitelisted(pod.Namespace) {
			if klog.V(4).Enabled() {
				klog.Infof("pod %s is in whitelisted namespace for rule %s", util.GetPodKey(pod), rule.Name)
			}
			continue
		}

		// validate required labels
		if err := m.validateRequiredLabels(pod, rule); err != nil {
			return fmt.Sprintf("validation rule '%s' failed: %v", rule.Name, err), err
		}
	}
	return "", nil
}

// validateRequiredLabels validates that all required labels are present
func (m *PodEnhancedValidator) validateRequiredLabels(pod *corev1.Pod, rule ValidationRule) error {
	var missingLabels []string

	for _, key := range rule.RequiredLabels {
		if _, exists := pod.Labels[key]; !exists {
			missingLabels = append(missingLabels, key)
		}
	}

	if len(missingLabels) > 0 {
		if len(missingLabels) == 1 {
			return fmt.Errorf("required label '%s' is missing", missingLabels[0])
		}
		return fmt.Errorf("required labels are missing: %v", missingLabels)
	}

	return nil
}

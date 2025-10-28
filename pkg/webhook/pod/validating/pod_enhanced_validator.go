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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const (
	ConfigKeyEnable = "enable"
	ConfigKeyRules  = "rules"

	// DefaultConfReconcileInterval is the default reconcile interval for config
	DefaultConfReconcileInterval = 5 * time.Minute
)

var (
	// PodEnhancedValidatorConfigNamespace defines the namespace for the PodEnhancedValidator configuration.
	PodEnhancedValidatorConfigNamespace = "koordinator-system"
	// PodEnhancedValidatorConfigName defines the name for the PodEnhancedValidator configuration.
	PodEnhancedValidatorConfigName = "pod-enhanced-validator-config"
	// PodEnhancedValidatorReconcileInterval defines the reconcile interval for the PodEnhancedValidator configuration.
	PodEnhancedValidatorReconcileInterval = DefaultConfReconcileInterval

	DefaultPodEnhancedValidatorConf = &PodEnhancedValidatorConfig{
		Enable: false,
		Rules:  []ValidationRule{},
	}
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
	fs.DurationVar(&PodEnhancedValidatorReconcileInterval, "pod-enhanced-validator-reconcile-interval",
		DefaultConfReconcileInterval, "The reconcile interval for the pod-enhanced-validator configuration.")
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
	client          client.Client
	config          *PodEnhancedValidatorConfig
	configName      string
	configNamespace string

	startOnce sync.Once
	sync.RWMutex
}

func NewPodEnhancedValidator(client client.Client) *PodEnhancedValidator {
	return &PodEnhancedValidator{
		client:          client,
		configName:      PodEnhancedValidatorConfigName,
		configNamespace: PodEnhancedValidatorConfigNamespace,
		config:          DefaultPodEnhancedValidatorConf,
	}
}

// ensureConfigSynced ensures the config synchronization is started (lazy loading)
func (m *PodEnhancedValidator) ensureConfigSynced() {
	m.startOnce.Do(func() {
		klog.V(4).Infof("Starting PodEnhancedValidator with lazy initialization")

		// sync configuration immediately
		if err := m.syncConfig(); err != nil {
			klog.Errorf("Failed to sync config during initialization: %v", err)
		}

		// start periodic configuration synchronization
		go wait.Until(func() {
			if err := m.syncConfig(); err != nil {
				klog.Errorf("Failed to sync config for pod-enhanced-validator: %v", err)
			}
		}, PodEnhancedValidatorReconcileInterval, wait.NeverStop)
	})
}

// syncConfig synchronizes the configuration from the ConfigMap
func (m *PodEnhancedValidator) syncConfig() error {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{
		Namespace: m.configNamespace,
		Name:      m.configName,
	}

	err := m.client.Get(context.Background(), key, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			// ConfigMap not found, reset to default config
			m.updateConfig(DefaultPodEnhancedValidatorConf)
			klog.Info("pod-enhanced-validator configmap not found, reset to default config")
			return nil
		}
		return err
	}

	m.handleConfigMapUpdate(cm)
	return nil
}

// handleConfigMapUpdate handles configmap update events
func (m *PodEnhancedValidator) handleConfigMapUpdate(cm *corev1.ConfigMap) {
	config, err := m.parseConfig(cm)
	if err != nil {
		klog.Errorf("failed to parse config for pod-enhanced-validator, err=%v", err)
		return
	}

	m.updateConfig(config)
}

func (m *PodEnhancedValidator) updateConfig(config *PodEnhancedValidatorConfig) {
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
	// lazy loading: start only when actually needed
	m.ensureConfigSynced()

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
	// skip validation for pods with the skip-enhanced-validation label
	if pod.Labels[extension.LabelPodSkipEnhancedValidation] == "true" {
		return "", nil
	}
	// validate pod against all rules
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

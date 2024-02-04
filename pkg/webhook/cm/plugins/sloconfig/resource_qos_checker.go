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

package sloconfig

import (
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

const (
	// InvalidPercentageValueMsg is an error message for value must in percentage range.
	InvalidPercentageValueMsg string = `must be percentage value just in 0-100'`
)

var _ ConfigChecker = &ResourceQOSChecker{}

type ResourceQOSChecker struct {
	cfg *configuration.ResourceQOSCfg
	CommonChecker
}

func NewResourceQOSChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *ResourceQOSChecker {
	checker := &ResourceQOSChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: configuration.ResourceQOSConfigKey, initStatus: NotInit}}
	if !checker.IsCfgNotEmptyAndChanged() && !needUnmarshal {
		return checker
	}
	if err := checker.initConfig(); err != nil {
		checker.initStatus = err.Error()
	} else {
		checker.initStatus = InitSuccess
	}
	return checker
}

func (c *ResourceQOSChecker) ConfigParamValid() error {
	if err := c.CheckByValidator(c.cfg); err != nil {
		return err
	}

	return CheckNetQosCfg(c.cfg, field.NewPath("ResourceQOSCfg"))
}

func CheckNetQosCfg(cfg *configuration.ResourceQOSCfg, fldPath *field.Path) error {
	if cfg == nil {
		return nil
	}

	// check netqos config is valid for cluster strategy
	if cfg.ClusterStrategy != nil {
		if cfg.ClusterStrategy.BEClass != nil {
			if err := CheckSubNetQos(fldPath.Child("ClusterStrategy").Child("BEClass"), cfg.ClusterStrategy.BEClass.NetworkQOS); err != nil {
				return err
			}
		}

		if cfg.ClusterStrategy.LSClass != nil {
			if err := CheckSubNetQos(fldPath.Child("ClusterStrategy").Child("LSClass"), cfg.ClusterStrategy.LSClass.NetworkQOS); err != nil {
				return err
			}
		}

		if cfg.ClusterStrategy.LSRClass != nil {
			if err := CheckSubNetQos(fldPath.Child("ClusterStrategy").Child("LSRClass"), cfg.ClusterStrategy.LSRClass.NetworkQOS); err != nil {
				return err
			}
		}
	}

	// check netqos config is valid for each node strategy
	for idx, nodeStrategy := range cfg.NodeStrategies {
		if nodeStrategy.BEClass != nil {
			if err := CheckSubNetQos(fldPath.Child("nodeStrategy").Child(strconv.Itoa(idx)).Child("BEClass"), nodeStrategy.BEClass.NetworkQOS); err != nil {
				return err
			}
		}
		if nodeStrategy.LSClass != nil {
			if err := CheckSubNetQos(fldPath.Child("nodeStrategy").Child(strconv.Itoa(idx)).Child("LSClass"), nodeStrategy.LSClass.NetworkQOS); err != nil {
				return err
			}
		}

		if nodeStrategy.LSRClass != nil {
			if err := CheckSubNetQos(fldPath.Child("nodeStrategy").Child(strconv.Itoa(idx)).Child("LSRClass"), nodeStrategy.LSRClass.NetworkQOS); err != nil {
				return err
			}
		}
	}

	return nil
}

func CheckSubNetQos(fldPath *field.Path, qos *v1alpha1.NetworkQOSCfg) error {
	if qos == nil {
		return nil
	}

	if errs := ValidatePercentageOrQuantity(qos.IngressRequest, fldPath.Child("IngressRequest")); len(errs) > 0 {
		return storage.NewInvalidError(errs)
	}
	if errs := ValidatePercentageOrQuantity(qos.IngressLimit, fldPath.Child("IngressLimit")); len(errs) > 0 {
		return storage.NewInvalidError(errs)
	}
	if errs := ValidatePercentageOrQuantity(qos.EgressRequest, fldPath.Child("EgressRequest")); len(errs) > 0 {
		return storage.NewInvalidError(errs)
	}
	if errs := ValidatePercentageOrQuantity(qos.EgressLimit, fldPath.Child("EgressLimit")); len(errs) > 0 {
		return storage.NewInvalidError(errs)
	}

	return nil
}

// ValidatePercentageOrQuantity tests if a given value is a valid percentage or
// quantity (defines in: https://github.com/kubernetes/apimachinery/blob/master/pkg/api/resource/quantity.go#L100-L111).
func ValidatePercentageOrQuantity(intOrPercent *intstr.IntOrString, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if intOrPercent == nil {
		return allErrs
	}

	switch intOrPercent.Type {
	case intstr.String:
		allErrs = append(allErrs, ValidateQuantityField(intOrPercent.StrVal, fldPath)...)
	case intstr.Int:
		allErrs = append(allErrs, ValidatePercentageField(intOrPercent.IntValue(), fldPath)...)
	default:
		allErrs = append(allErrs, field.Invalid(fldPath, intOrPercent, "must be an integer just in 0-100 or quantity (e.g '50M')"))
	}

	return allErrs
}

// ValidateQuantityField validates that given value is quantity format, like 50M.
func ValidateQuantityField(quantityStr string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if _, err := resource.ParseQuantity(quantityStr); err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, quantityStr, err.Error()))
	}
	return allErrs
}

// ValidatePercentageField validates that given value is a percentage format just in 0-100.
func ValidatePercentageField(value int, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if value < 0 || value > 100 {
		allErrs = append(allErrs, field.Invalid(fldPath, value, InvalidPercentageValueMsg))
	}
	return allErrs
}

func (c *ResourceQOSChecker) initConfig() error {
	cfg := &configuration.ResourceQOSCfg{}
	configStr := c.NewConfigMap.Data[configuration.ResourceQOSConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse ResourceQOS config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error())
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(configuration.ResourceQOSConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse ResourceQOS config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error()))
		return err
	}
	return nil
}

func (c *ResourceQOSChecker) getConfigProfiles() []configuration.NodeCfgProfile {
	var profiles []configuration.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeStrategies {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}

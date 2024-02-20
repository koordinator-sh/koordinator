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
	"errors"
	"fmt"
	"reflect"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// NetDirection represents the stored type of IngressOrEgress
type NetDirection int64

const (
	Ingress NetDirection = iota // The IngressOrEgress holds an ingress.
	Egress                      // The IngressOrEgress holds an egress.
)

var _ ConfigChecker = &ResourceQOSChecker{}

type ResourceQOSChecker struct {
	cfg    *configuration.ResourceQOSCfg
	sysCfg *configuration.SystemCfg
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

	return CheckNetQosCfg(c.cfg, c.sysCfg, field.NewPath("ResourceQOSCfg"))
}

func CheckNetQosCfg(cfg *configuration.ResourceQOSCfg, sysCfg *configuration.SystemCfg, fldPath *field.Path) error {
	if cfg == nil || sysCfg == nil {
		return nil
	}

	if err := CheckEachElementIsValid(cfg, fldPath); err != nil {
		return err
	}

	if err := CheckOnlyOneFormat(cfg); err != nil {
		return err
	}

	if err := CheckTotalPercentageIsValid(cfg); err != nil {
		return err
	}

	return CheckTotalQuantityIsValid(cfg, sysCfg)
}

func CheckEachElementIsValid(cfg *configuration.ResourceQOSCfg, fldPath *field.Path) error {
	doCheck := func(fldPath *field.Path, strategy *v1alpha1.ResourceQOSStrategy) error {
		if strategy == nil {
			return nil
		}

		if strategy.LSRClass != nil {
			if err := CheckSubNetQos(fldPath.Child("LSRClass"), strategy.LSRClass.NetworkQOS); err != nil {
				return err
			}
		}

		if strategy.LSClass != nil {
			if err := CheckSubNetQos(fldPath.Child("LSClass"), strategy.LSClass.NetworkQOS); err != nil {
				return err
			}
		}
		if strategy.BEClass != nil {
			if err := CheckSubNetQos(fldPath.Child("BEClass"), strategy.BEClass.NetworkQOS); err != nil {
				return err
			}
		}

		return nil
	}

	// check netqos config is valid for cluster strategy
	if cfg.ClusterStrategy != nil {
		if err := doCheck(fldPath.Child("ClusterStrategy"), cfg.ClusterStrategy); err != nil {
			return err
		}
	}

	// check netqos config is valid for each node strategy
	for idx, nodeStrategy := range cfg.NodeStrategies {
		if err := doCheck(fldPath.Child("nodeStrategy").Child(strconv.Itoa(idx)), nodeStrategy.ResourceQOSStrategy); err != nil {
			return err
		}
	}

	return nil
}

func CheckTotalPercentageIsValid(cfg *configuration.ResourceQOSCfg) error {
	// check netqos config is valid for cluster strategy
	if cfg.ClusterStrategy != nil {
		var totalPercentage int64 = 100
		ingressLeft, egressLeft := doSubstration(cfg.ClusterStrategy, totalPercentage)
		if ingressLeft < 0 || egressLeft < 0 {
			return fmt.Errorf("ClusterStrategy total net bandwidth percentage already over 100")
		}
	}

	// check netqos config is valid for each node strategy
	for idx, nodeStrategy := range cfg.NodeStrategies {
		var totalPercentage int64 = 100
		ingressLeft, egressLeft := doSubstration(nodeStrategy.ResourceQOSStrategy, totalPercentage)
		if ingressLeft < 0 || egressLeft < 0 {
			return fmt.Errorf("cfg.NodeStrategies[%d] total net bandwidth percentage already over 100", idx)
		}
	}

	return nil
}

func doSubstration(strategy *v1alpha1.ResourceQOSStrategy, totalNetBandWidth int64) (leftIngress, leftEgress int64) {
	ingressRequestTotal, egressRequestTotal := totalNetBandWidth, totalNetBandWidth
	if strategy == nil {
		return ingressRequestTotal, egressRequestTotal
	}

	subFunc := func(direction NetDirection, request *intstr.IntOrString) {
		if request == nil {
			return
		}

		if direction == Ingress {
			if request.Type == intstr.Int {
				ingressRequestTotal -= int64(request.IntValue())
			}

			if request.Type == intstr.String {
				if cur, err := resource.ParseQuantity(request.StrVal); err == nil {
					ingressRequestTotal -= cur.Value()
				}
			}
		}
		if direction == Egress {
			if request.Type == intstr.Int {
				egressRequestTotal -= int64(request.IntValue())
			}

			if request.Type == intstr.String {
				if cur, err := resource.ParseQuantity(request.StrVal); err == nil {
					egressRequestTotal -= cur.Value()
				}
			}
		}
	}

	if strategy.LSRClass != nil && strategy.LSRClass.NetworkQOS != nil {
		subFunc(Ingress, strategy.LSRClass.NetworkQOS.IngressRequest)
		subFunc(Egress, strategy.LSRClass.NetworkQOS.EgressRequest)
	}
	if strategy.LSClass != nil && strategy.LSClass.NetworkQOS != nil {
		subFunc(Ingress, strategy.LSClass.NetworkQOS.IngressRequest)
		subFunc(Egress, strategy.LSClass.NetworkQOS.EgressRequest)
	}
	if strategy.BEClass != nil && strategy.BEClass.NetworkQOS != nil {
		subFunc(Ingress, strategy.BEClass.NetworkQOS.IngressRequest)
		subFunc(Egress, strategy.BEClass.NetworkQOS.EgressRequest)
	}

	return ingressRequestTotal, egressRequestTotal
}

func CheckTotalQuantityIsValid(cfg *configuration.ResourceQOSCfg, sysCfg *configuration.SystemCfg) error {
	// check netqos config is valid for cluster strategy
	if cfg.ClusterStrategy != nil && sysCfg.ClusterStrategy != nil {
		ingressLeft, egressLeft := doSubstration(cfg.ClusterStrategy, sysCfg.ClusterStrategy.TotalNetworkBandwidth.Value())
		if ingressLeft < 0 || egressLeft < 0 {
			return fmt.Errorf("total net bandwidth over total network bandwidth in clusterStrategy")
		}
	}

	// check netqos config is valid for each node strategy
	for idx, nodeStrategy := range cfg.NodeStrategies {
		totalNetbandWidth := getTotalNetBandWidth(nodeStrategy.NodeSelector, sysCfg)
		ingressLeft, egressLeft := doSubstration(nodeStrategy.ResourceQOSStrategy, totalNetbandWidth.Value())
		if ingressLeft < 0 || egressLeft < 0 {
			return fmt.Errorf("total net bandwidth quantity already over total network bandwidth in cfg.NodeStrategies[%d]", idx)
		}

	}

	return nil
}

func getTotalNetBandWidth(labelSelector *metav1.LabelSelector, sysCfg *configuration.SystemCfg) *resource.Quantity {
	res := resource.NewQuantity(0, resource.DecimalSI)
	if sysCfg == nil {
		return res
	}

	if len(sysCfg.NodeStrategies) == 0 && sysCfg.ClusterStrategy != nil {
		return &sysCfg.ClusterStrategy.TotalNetworkBandwidth
	}

	for _, cur := range sysCfg.NodeStrategies {
		if reflect.DeepEqual(labelSelector, cur.NodeSelector) {
			return &cur.TotalNetworkBandwidth
		}
	}

	return res
}

func CheckOnlyOneFormat(cfg *configuration.ResourceQOSCfg) error {
	getFormatsFunc := func(strategy *v1alpha1.ResourceQOSStrategy) int {
		allTypes := make(map[intstr.Type]interface{})
		if strategy == nil {
			return len(allTypes)
		}

		mark := func(request *intstr.IntOrString) {
			if request == nil {
				return
			}
			allTypes[request.Type] = nil
		}

		if strategy.LSRClass != nil && strategy.LSRClass.NetworkQOS != nil {
			mark(strategy.LSRClass.NetworkQOS.IngressRequest)
			mark(strategy.LSRClass.NetworkQOS.EgressRequest)
		}
		if strategy.LSClass != nil && strategy.LSClass.NetworkQOS != nil {
			mark(strategy.LSClass.NetworkQOS.IngressRequest)
			mark(strategy.LSClass.NetworkQOS.EgressRequest)
		}
		if strategy.BEClass != nil && strategy.BEClass.NetworkQOS != nil {
			mark(strategy.BEClass.NetworkQOS.IngressRequest)
			mark(strategy.BEClass.NetworkQOS.EgressRequest)
		}

		return len(allTypes)
	}

	// check netqos config is valid for cluster strategy
	if cfg.ClusterStrategy != nil {
		allTypes := getFormatsFunc(cfg.ClusterStrategy)
		if allTypes > 1 {
			return errors.New("only one type can be used for percentages or quantity in clusterStrategy")
		}
	}

	// check netqos config is valid for each node strategy
	for idx, nodeStrategy := range cfg.NodeStrategies {
		allTypes := getFormatsFunc(nodeStrategy.ResourceQOSStrategy)
		if allTypes > 1 {
			return fmt.Errorf("only one type can be used for percentages or quantity in nodeStrategy[%d]", idx)
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

	sysCfg := &configuration.SystemCfg{}
	sysConfigStr := c.NewConfigMap.Data[configuration.SystemConfigKey]
	err = json.Unmarshal([]byte(sysConfigStr), &sysCfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse System config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error())
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.sysCfg = sysCfg

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

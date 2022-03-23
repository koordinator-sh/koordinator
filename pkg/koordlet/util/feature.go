package util

import (
	"reflect"
	"runtime"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
)

// RunFeature runs moduleFunc only if interval > 0 AND at least one feature dependency is enabled
func RunFeature(moduleFunc func(), featureDependency []featuregate.Feature, interval int, stopCh <-chan struct{}) bool {
	ret, _ := RunFeatureWithInit(func() error { return nil }, moduleFunc, featureDependency, interval, stopCh)
	return ret
}

// RunFeatureWithInit runs moduleFunc only if interval > 0 , at least one feature dependency is enabled
// and moduleInit function returns nil
func RunFeatureWithInit(moduleInit func() error, moduleFunc func(), featureDependency []featuregate.Feature, interval int, stopCh <-chan struct{}) (bool, error) {
	moduleInitName := runtime.FuncForPC(reflect.ValueOf(moduleInit).Pointer()).Name()
	moduleFuncName := runtime.FuncForPC(reflect.ValueOf(moduleFunc).Pointer()).Name()
	if interval <= 0 {
		klog.Infof("time interval %v is disabled, skip run %v module", interval, moduleFuncName)
		return false, nil
	}

	moduleFuncEnabled := len(featureDependency) == 0
	for _, feature := range featureDependency {
		if features.DefaultKoordletFeatureGate.Enabled(feature) {
			moduleFuncEnabled = true
			break
		}
	}
	if !moduleFuncEnabled {
		klog.Infof("all feature dependency %v is disabled, skip run module %v", featureDependency, moduleFuncName)
		return false, nil
	}

	klog.Infof("starting %v feature init module", moduleInitName)
	if err := moduleInit(); err != nil {
		klog.Errorf("starting %v feature init module error %v", moduleInitName, err)
		return false, err
	}

	klog.Infof("starting %v feature dependency module, interval seconds %v", moduleFuncName, interval)
	go wait.Until(moduleFunc, time.Duration(interval)*time.Second, stopCh)
	return true, nil
}

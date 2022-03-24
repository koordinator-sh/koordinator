package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

func init() {
	runtime.Must(defaultMutableFeatureGate.Add(defaultControllerFeatureGates))
}

const (
	// NodeMetricControl is responsible for NodeMetric CR reconciliation
	NodeMetricControl featuregate.Feature = "NodeMetricControl"
	// NodeResourceControl is responsible for node BE allocatable resource calculation and reporting
	NodeResourceControl featuregate.Feature = "NodeResourceControl"
)

var (
	defaultMutableFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
	DefaultFeatureGate        featuregate.FeatureGate        = defaultMutableFeatureGate

	defaultControllerFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
		NodeMetricControl:   {Default: true, PreRelease: featuregate.Beta},
		NodeResourceControl: {Default: false, PreRelease: featuregate.Alpha},
	}
)

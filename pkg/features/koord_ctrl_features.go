package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// NodeMetricControl is responsible for NodeMetric CR reconciliation
	NodeMetricControl featuregate.Feature = "NodeMetricControl"
	// NodeResourceControl is responsible for node BE allocatable resource calculation and reporting
	NodeResourceControl featuregate.Feature = "NodeResourceControl"
)

func init() {
	runtime.Must(defaultKoordCtrlMutableFeatureGate.Add(defaultKoordCtrlFeatureGates))
}

var (
	defaultKoordCtrlMutableFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
	DefaultKoordCtlFeatureGate         featuregate.FeatureGate        = defaultKoordCtrlMutableFeatureGate

	defaultKoordCtrlFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
		NodeMetricControl:   {Default: true, PreRelease: featuregate.Beta},
		NodeResourceControl: {Default: false, PreRelease: featuregate.Alpha},
	}
)

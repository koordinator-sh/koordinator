package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// AuditEvents is used to audit recent events
	AuditEvents featuregate.Feature = "AuditEvents"

	// AuditEventsHTTPHandler is used to get recent events from koordlet port
	AuditEventsHTTPHandler featuregate.Feature = "AuditEventsHTTPHandler"
)

func init() {
	runtime.Must(DefaultMutableKoordletFeatureGate.Add(defaultKoordletFeatureGates))
}

var (
	DefaultMutableKoordletFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
	DefaultKoordletFeatureGate        featuregate.FeatureGate        = DefaultMutableKoordletFeatureGate

	defaultKoordletFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
		AuditEvents:            {Default: false, PreRelease: featuregate.Alpha},
		AuditEventsHTTPHandler: {Default: false, PreRelease: featuregate.Alpha},
	}
)

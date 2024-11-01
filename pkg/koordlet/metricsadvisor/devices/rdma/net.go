package rdma

import (
	"strconv"

	"k8s.io/klog"
)

const (
	classIDBaseInt = 16
	classIDBitSize = 64
	netDevClassID  = 0x02
)

func IsNetDevice(devClassID string) bool {
	devClass, err := parseDeviceClassID(devClassID)
	if err != nil {
		klog.Warningf("getNetDevice(): unable to parse device class for device %+v %q", devClassID, err)
		return false
	}
	return devClass == netDevClassID
}

// parseDeviceClassID returns device ID parsed from the string as 64bit integer
func parseDeviceClassID(deviceID string) (int64, error) {
	return strconv.ParseInt(deviceID, classIDBaseInt, classIDBitSize)
}

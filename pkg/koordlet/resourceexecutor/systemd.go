/*
Copyright 2026 The Koordinator Authors.

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

package resourceexecutor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	systemddbus "github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
	"k8s.io/klog/v2"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const systemdSetUnitPropertiesTimeout = 5 * time.Second

var errSystemdDBusUnavailable = errors.New("systemd dbus is unavailable")

var kubeQOSSystemdUnits = map[string]string{
	"kubepods.slice": "kubepods.slice",
	"kubepods.slice/kubepods-burstable.slice":  "kubepods-burstable.slice",
	"kubepods.slice/kubepods-besteffort.slice": "kubepods-besteffort.slice",
}

type systemdUnitPropertyWriter interface {
	SetUnitProperties(ctx context.Context, unitName string, runtime bool, properties ...systemddbus.Property) error
}

type systemdDBusPropertyWriter struct {
	lock sync.Mutex
	conn *systemddbus.Conn
}

func (w *systemdDBusPropertyWriter) SetUnitProperties(ctx context.Context, unitName string, runtime bool, properties ...systemddbus.Property) error {
	conn, err := w.getConn(ctx)
	if err != nil {
		return err
	}
	return conn.SetUnitPropertiesContext(ctx, unitName, runtime, properties...)
}

func (w *systemdDBusPropertyWriter) getConn(ctx context.Context) (*systemddbus.Conn, error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.conn != nil {
		return w.conn, nil
	}
	address := systemBusAddress()
	if address == "" {
		return nil, errSystemdDBusUnavailable
	}
	conn, err := newSystemdConnection(ctx, address)
	if err != nil {
		return nil, err
	}
	w.conn = conn
	return conn, nil
}

var systemdPropertyWriter systemdUnitPropertyWriter = &systemdDBusPropertyWriter{}

func setSystemdPropertyWriterForTest(writer systemdUnitPropertyWriter) func() {
	oldWriter := systemdPropertyWriter
	systemdPropertyWriter = writer
	return func() {
		systemdPropertyWriter = oldWriter
	}
}

func updateSystemdUnitPropertyForCgroup(c *CgroupResourceUpdater, value string) (bool, error) {
	unitName, ok := systemdQOSUnitName(c.parentDir)
	if !ok {
		return false, nil
	}

	property, ok, err := systemdPropertyForCgroupResource(c.ResourceType(), value)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), systemdSetUnitPropertiesTimeout)
	defer cancel()

	if err := systemdPropertyWriter.SetUnitProperties(ctx, unitName, true, property); errors.Is(err, errSystemdDBusUnavailable) {
		klog.V(5).Infof("skip setting systemd unit %s property %s since system dbus is unavailable", unitName, property.Name)
		return true, nil
	} else if err != nil {
		klog.V(4).Infof("failed to set systemd unit %s property %s, direct cgroup write has been applied, err: %v",
			unitName, property.Name, err)
		return true, nil
	}
	klog.V(6).Infof("successfully set systemd unit %s property %s to %v", unitName, property.Name, property.Value.Value())
	return true, nil
}

func newSystemdConnection(ctx context.Context, address string) (*systemddbus.Conn, error) {
	return systemddbus.NewConnection(func() (*godbus.Conn, error) {
		conn, err := godbus.Dial(address, godbus.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		methods := []godbus.Auth{godbus.AuthExternal(strconv.Itoa(os.Getuid()))}
		if err = conn.Auth(methods); err != nil {
			conn.Close()
			return nil, err
		}
		if err = conn.Hello(); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	})
}

func systemBusAddress() string {
	if address := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS"); address != "" {
		return address
	}
	for _, socketPath := range []string{
		filepath.Join(sysutil.Conf.RunRootDir, "dbus/system_bus_socket"),
		filepath.Join(sysutil.Conf.VarRunRootDir, "dbus/system_bus_socket"),
		"/run/dbus/system_bus_socket",
		"/var/run/dbus/system_bus_socket",
	} {
		if _, err := os.Stat(socketPath); err == nil {
			return "unix:path=" + socketPath
		}
	}
	return ""
}

func systemdQOSUnitName(parentDir string) (string, bool) {
	cleaned := filepath.ToSlash(filepath.Clean(parentDir))
	cleaned = strings.Trim(cleaned, "/")
	unitName, ok := kubeQOSSystemdUnits[cleaned]
	return unitName, ok
}

func systemdPropertyForCgroupResource(resourceType sysutil.ResourceType, value string) (systemddbus.Property, bool, error) {
	switch resourceType {
	case sysutil.CPUCFSQuotaName:
		v, err := cpuQuotaToCPUQuotaPerSecUSec(value)
		if err != nil {
			return systemddbus.Property{}, false, err
		}
		return newSystemdProperty("CPUQuotaPerSecUSec", v), true, nil
	case sysutil.MemoryMinName:
		v, err := memoryValueToSystemdValue(value)
		if err != nil {
			return systemddbus.Property{}, false, err
		}
		return newSystemdProperty("MemoryMin", v), true, nil
	case sysutil.MemoryLowName:
		v, err := memoryValueToSystemdValue(value)
		if err != nil {
			return systemddbus.Property{}, false, err
		}
		return newSystemdProperty("MemoryLow", v), true, nil
	case sysutil.MemoryHighName:
		v, err := memoryValueToSystemdValue(value)
		if err != nil {
			return systemddbus.Property{}, false, err
		}
		return newSystemdProperty("MemoryHigh", v), true, nil
	default:
		return systemddbus.Property{}, false, nil
	}
}

func newSystemdProperty(name string, value interface{}) systemddbus.Property {
	return systemddbus.Property{
		Name:  name,
		Value: godbus.MakeVariant(value),
	}
}

func cpuQuotaToCPUQuotaPerSecUSec(value string) (uint64, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, fmt.Errorf("cpu quota is empty")
	}

	quota := fields[0]
	if quota == sysutil.CgroupMaxSymbolStr || quota == sysutil.CgroupUnlimitedSymbolStr {
		return uint64(math.MaxUint64), nil
	}

	quotaUSec, err := strconv.ParseInt(quota, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cpu quota %q is not int64: %w", quota, err)
	}
	if quotaUSec <= 0 {
		return uint64(math.MaxUint64), nil
	}

	periodUSec := sysutil.DefaultCPUCFSPeriod
	if len(fields) > 1 {
		periodUSec, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("cpu quota period %q is not int64: %w", fields[1], err)
		}
	}
	if periodUSec <= 0 {
		return 0, fmt.Errorf("cpu quota period %d is invalid", periodUSec)
	}

	return uint64(quotaUSec) * uint64(time.Second/time.Microsecond) / uint64(periodUSec), nil
}

func memoryValueToSystemdValue(value string) (uint64, error) {
	v := strings.TrimSpace(value)
	if v == sysutil.CgroupMaxSymbolStr || v == sysutil.CgroupUnlimitedSymbolStr {
		return uint64(math.MaxUint64), nil
	}
	memoryValue, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("memory value %q is not uint64: %w", v, err)
	}
	return memoryValue, nil
}

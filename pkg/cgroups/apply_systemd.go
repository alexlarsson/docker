package cgroups

import (
	"fmt"
	"github.com/dotcloud/docker/pkg/systemd"
	"path/filepath"
	"strconv"
	"strings"
)

type systemdCgroup struct {
}

func useSystemd() bool {
	if !systemd.SdBooted() {
		return false
	}
	manager, _ := systemd.GetManager()

	return manager != nil && manager.HasStartTransientUnit
}

func getIfaceForUnit(unitName string) string {
	if strings.HasSuffix(unitName, ".scope") {
		return "org.freedesktop.systemd1.Scope"
	}
	if strings.HasSuffix(unitName, ".service") {
		return "org.freedesktop.systemd1.Service"
	}
	return "org.freedesktop.systemd1.Unit"
}

// This is kind of a hack which moves a process into the
// cgroup of an existing unit. We do this by trying
// to add the pid to each possible cgroup with the
// systemd cgroup path. We join the name=systemd
// cgroup first, to ensure that on any races systemd
// will think the process is in that cgroup.
func joinUnit(unit *systemd.Unit, unitName string, pid int) error {
	cgroup, err := unit.GetProperty(getIfaceForUnit(unitName), "ControlGroup")
	if err != nil {
		return err
	}

	subsystems, err := GetAllSubsystems()
	if err != nil {
		return err
	}

	// Loop in reverse order to do the name=systemd one first
	for i := len(subsystems) - 1; i >= 0; i-- {
		subsyst := subsystems[i]
		mountpoint, _ := FindCgroupMountpoint(subsyst)
		if mountpoint != "" {
			path := filepath.Join(mountpoint, cgroup.(string))
			writeFile(path, "cgroup.procs", strconv.Itoa(pid))
		}
	}
	return nil
}

func systemdApply(c *Cgroup, pid int) (ActiveCgroup, error) {
	unitName := c.Parent + "-" + c.Name + ".scope"
	slice := "system.slice"
	reuseUnit := false

	properties := []systemd.Property{}

	for _, v := range c.UnitProperties {
		switch v[0] {
		case "Slice":
			slice = v[1]
		case "ReuseUnit":
			reuseUnit = true
			unitName = v[1]
		default:
			return nil, fmt.Errorf("Unknown unit propery %s", v[0])
		}
	}

	if !reuseUnit {
		properties = append(properties,
			systemd.Property{"Slice", slice},
			systemd.Property{"Description", "docker container " + c.Name},
			systemd.Property{"PIDs", []uint32{uint32(pid)}})
	}

	if !c.DeviceAccess {
		properties = append(properties,
			systemd.Property{"DevicePolicy", "strict"},
			systemd.Property{"DeviceAllow", []systemd.DeviceAllow{
				{"/dev/null", "rwm"},
				{"/dev/zero", "rwm"},
				{"/dev/full", "rwm"},
				{"/dev/random", "rwm"},
				{"/dev/urandom", "rwm"},
				{"/dev/tty", "rwm"},
				{"/dev/console", "rwm"},
				{"/dev/tty0", "rwm"},
				{"/dev/tty1", "rwm"},
				{"/dev/pts/ptmx", "rwm"},
				// There is no way to add /dev/pts/* here atm, so we hack this manually below
				// /dev/pts/* (how to add this?)
				// Same with tuntap, which doesn't exist as a node most of the time
			}})
	}

	if c.Memory != 0 {
		properties = append(properties,
			systemd.Property{"MemoryLimit", uint64(c.Memory)})
	}
	// TODO: MemorySwap not available in systemd

	if c.CpuShares != 0 {
		properties = append(properties,
			systemd.Property{"CPUShares", uint64(c.CpuShares)})
	}
	manager, err := systemd.GetManager()
	if err != nil {
		return nil, err
	}

	var unit *systemd.Unit

	if reuseUnit {
		unit, err = manager.GetUnit(unitName)
		if err != nil {
			return nil, err
		}

		// Before we change any properties w switch the pid into the cgroups of
		// the existing unit. That way any rebuilding of the cgroup triggered by
		// the property change happens after we joined.
		if err := joinUnit(unit, unitName, pid); err != nil {
			return nil, err
		}

		if err := manager.SetUnitProperties(unitName, true, properties); err != nil {
			return nil, err
		}
	} else {
		if err := manager.StartTransientUnit(unitName, "replace", properties); err != nil {
			return nil, err
		}

		unit, err = manager.GetUnit(unitName)
		if err != nil {
			return nil, err
		}
	}

	// To work around the lack of /dev/pts/* support above we need to manually add these
	// so, ask systemd for the cgroup used
	cgroup, err := unit.GetProperty(getIfaceForUnit(unitName), "ControlGroup")
	if err != nil {
		return nil, err
	}

	if !c.DeviceAccess {
		mountpoint, err := FindCgroupMountpoint("devices")
		if err != nil {
			return nil, err
		}

		path := filepath.Join(mountpoint, cgroup.(string))

		// /dev/pts/*
		if err := writeFile(path, "devices.allow", "c 136:* rwm"); err != nil {
			return nil, err
		}
		// tuntap
		if err := writeFile(path, "devices.allow", "c 10:200 rwm"); err != nil {
			return nil, err
		}
	}

	return &systemdCgroup{}, nil
}

func (c *systemdCgroup) Cleanup() error {
	// systemd cleans up, we don't need to do anything
	return nil
}

package cube

import (
	"errors"
	"fmt"
	"net"
)

// ResourceBudget separates physical CPU reservations from burstable guest vCPU
// allocations. HostCPUMillis still describes the native scheduler's vCPU quota;
// the worker cgroup enforces the physical CPU pool configured by the operator.
// Memory is never overcommitted: every charged guest reserves its full limit
// plus VM overhead, including uncertain lifecycle operations.
type ResourceBudget struct {
	CPUMillis    int                        `json:"cpu_millis"`
	MemoryMB     int                        `json:"memory_mb"`
	RuntimeSlots int                        `json:"runtime_slots"`
	BuildSlots   int                        `json:"build_slots"`
	Profiles     map[string]ResourceProfile `json:"profiles"`
}

type ResourceProfile struct {
	CPUMillis      int    `json:"cpu_millis"`
	WritableDiskMB int    `json:"writable_disk_mb"`
	Kind           string `json:"kind"` // runtime or build
}

const VMOverheadMB = 128

func (c AdmissionConfig) validateResourceBudget() error {
	b := c.ResourceBudget
	if b == nil || (validateID(c.NodeID) != nil && net.ParseIP(c.NodeID) == nil) || c.NodeID == "" {
		return errors.New("resource budgets require a pinned worker")
	}
	if c.CPUCount != 0 || c.MemoryMB != 0 || c.WritableDiskMB != 0 {
		return errors.New("resource budgets cannot be combined with a uniform resource profile")
	}
	if b.RuntimeSlots < 1 || b.BuildSlots < 0 || b.BuildSlots > 2 || c.MaxActive != b.RuntimeSlots+b.BuildSlots || c.MaxActive > MaxPinnedWorkerActive {
		return errors.New("invalid runtime/build slot budget")
	}
	if b.CPUMillis < 100 || b.CPUMillis > c.HostCPUMillis || c.HostCPUMillis > b.CPUMillis*16 || b.MemoryMB < 512+VMOverheadMB || b.MemoryMB > c.HostMemoryMB {
		return errors.New("resource budget exceeds worker quota")
	}
	if len(c.Templates) == 0 || len(b.Profiles) != len(c.Templates) {
		return errors.New("every template requires an explicit resource profile")
	}
	for id, r := range c.Templates {
		p, ok := b.Profiles[id]
		if !ok || validateID(id) != nil || r.CPUCount < 1 || r.CPUCount > 4 || r.MemoryMB < 512 || r.MemoryMB > 8192 || r.MemoryMB%256 != 0 || r.MemoryMB+VMOverheadMB > b.MemoryMB {
			return fmt.Errorf("invalid resource contract for template %s", id)
		}
		if p.CPUMillis < 100 || p.CPUMillis > r.CPUCount*1000 || p.CPUMillis > b.CPUMillis || p.WritableDiskMB < 1024 || p.WritableDiskMB > 32768 || p.WritableDiskMB%1024 != 0 {
			return fmt.Errorf("invalid CPU/disk reservation for template %s", id)
		}
		if p.Kind != "runtime" && p.Kind != "build" || p.Kind == "build" && b.BuildSlots == 0 {
			return errors.New("invalid workload class")
		}
	}
	return nil
}

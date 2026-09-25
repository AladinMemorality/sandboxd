package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// ResourceLimits uses exact integer units. An absent/unlimited source limit is
// not silently converted to a finite template allowance.
type ResourceLimits struct {
	CPUMilli    int64 `json:"cpu_milli"`
	MemoryBytes int64 `json:"memory_bytes"`
}

func (r ResourceLimits) valid() bool {
	return r.CPUMilli > 0 && r.CPUMilli <= 4096000 && r.MemoryBytes > 0 && r.MemoryBytes <= 1<<50
}
func (r ResourceLimits) covers(source ResourceLimits) bool {
	return r.valid() && source.valid() && r.CPUMilli >= source.CPUMilli && r.MemoryBytes >= source.MemoryBytes
}

// ReadTemplateResources loads operator-reviewed template identities, not preset
// aliases. Actual created VM resources are verified against this contract too.
func ReadTemplateResources(path string) (map[string]ResourceLimits, error) {
	out := map[string]ResourceLimits{}
	if path == "" {
		return out, nil
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("template resources must be bounded regular JSON")
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&out); e != nil {
		return nil, e
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || out == nil {
		return nil, errors.New("invalid template resources map")
	}
	for id, limits := range out {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(id) || !limits.valid() {
			return nil, errors.New("invalid template resource identity or limits")
		}
	}
	return out, nil
}
func sourceResources(container *docker.ContainerJSON) (ResourceLimits, error) {
	if container == nil {
		return ResourceLimits{}, errors.New("source resource inspection missing")
	}
	h := container.HostConfig
	cpu := int64(0)
	if h.NanoCPUs > 0 {
		cpu = int64(math.Ceil(float64(h.NanoCPUs) / 1e6))
	} else if h.CPUQuota > 0 && h.CPUPeriod > 0 {
		cpu = int64(math.Ceil(float64(h.CPUQuota) * 1000 / float64(h.CPUPeriod)))
	}
	out := ResourceLimits{CPUMilli: cpu, MemoryBytes: h.Memory}
	if !out.valid() {
		return out, errors.New("source CPU/RAM limits are absent, unlimited or unsupported; explicit capacity review required")
	}
	return out, nil
}
func targetResources(sandbox *cube.Sandbox) (ResourceLimits, error) {
	if sandbox == nil || sandbox.CPUCount < 1 || sandbox.CPUCount > 4096 || sandbox.MemoryMB < 1 || sandbox.MemoryMB > 1<<30 {
		return ResourceLimits{}, errors.New("Cube API omitted valid target CPU/RAM metadata")
	}
	return ResourceLimits{CPUMilli: int64(sandbox.CPUCount) * 1000, MemoryBytes: int64(sandbox.MemoryMB) * (1 << 20)}, nil
}

type migrationResourceContract struct {
	Version           int            `json:"version"`
	SandboxID         string         `json:"sandbox_id"`
	SourceContainerID string         `json:"source_container_id"`
	TemplateID        string         `json:"template_id"`
	Source            ResourceLimits `json:"source"`
	Target            ResourceLimits `json:"target"`
}

func (b *OfflineBackend) resourceContractPath(m *store.RuntimeMigration) (string, error) {
	p, e := b.artifact(m, "source")
	if e != nil {
		return "", e
	}
	return filepath.Join(filepath.Dir(p), "resource-contract.json"), nil
}
func (b *OfflineBackend) readPinnedResourceContract(m *store.RuntimeMigration) (migrationResourceContract, error) {
	var c migrationResourceContract
	path, e := b.resourceContractPath(m)
	if e != nil {
		return c, e
	}
	file, e := os.Open(path)
	if e != nil {
		return c, fmt.Errorf("durable migration resource contract unavailable: %w", e)
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return c, errors.New("resource contract is not a bounded regular file")
	}
	raw, e := io.ReadAll(io.LimitReader(file, 4097))
	if e != nil {
		return c, e
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.SandboxID != m.SandboxID || !sourceContainerIdentityMatches(m.Source.ContainerID.String, c.SourceContainerID) || c.TemplateID != m.Binding.TemplateID || !c.Target.covers(c.Source) {
		return c, errors.New("invalid durable migration resource contract")
	}
	return c, nil
}

func (b *OfflineBackend) readResourceContract(m *store.RuntimeMigration) (migrationResourceContract, error) {
	c, err := b.readPinnedResourceContract(m)
	if err != nil {
		return c, err
	}
	expected, ok := b.TemplateResources[c.TemplateID]
	if !ok || expected != c.Target {
		return c, errors.New("template resource mapping is missing or differs from durable migration contract")
	}
	return c, nil
}

// Historical rows may store Docker's twelve-character ID. Only a canonical
// hexadecimal prefix of the resolved immutable ID is accepted, never a name.
// The durable contract always pins the full inspected ID before source stop.
func sourceContainerIdentityMatches(recorded, inspected string) bool {
	if recorded == "" || inspected == "" {
		return false
	}
	if recorded == inspected {
		return true
	}
	return regexp.MustCompile(`^[a-f0-9]{12}$`).MatchString(recorded) &&
		regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(inspected) && strings.HasPrefix(inspected, recorded)
}

// ValidateResourceSelection is read-only and can reject a bad plan before a
// migration journal or maintenance-owned source stop is created.
func ValidateResourceSelection(templateID, containerID string, templates map[string]ResourceLimits, inspected *docker.ContainerJSON) error {
	if inspected == nil || !sourceContainerIdentityMatches(containerID, inspected.ID) {
		return errors.New("source resource inspection identity mismatch")
	}
	source, e := sourceResources(inspected)
	if e != nil {
		return e
	}
	target, ok := templates[templateID]
	if !ok || !target.valid() {
		return errors.New("reviewed template resource contract is required before stopping source")
	}
	if !target.covers(source) {
		return errors.New("target template would reduce source CPU or memory limits")
	}
	return nil
}

func (b *OfflineBackend) preserveSourceResources(m *store.RuntimeMigration, inspected *docker.ContainerJSON) error {
	if !m.Source.ContainerID.Valid {
		return errors.New("source container identity is missing")
	}
	if e := ValidateResourceSelection(m.Binding.TemplateID, m.Source.ContainerID.String, b.TemplateResources, inspected); e != nil {
		return e
	}
	source, _ := sourceResources(inspected)
	target := b.TemplateResources[m.Binding.TemplateID]
	c := migrationResourceContract{Version: 1, SandboxID: m.SandboxID, SourceContainerID: inspected.ID, TemplateID: m.Binding.TemplateID, Source: source, Target: target}
	path, e := b.resourceContractPath(m)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(path); e == nil {
		old, e := b.readResourceContract(m)
		if e != nil {
			return e
		}
		if old != c {
			return errors.New("source resources changed since migration planning")
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return e
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".resources-")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, e = file.Write(raw); e != nil {
		return e
	}
	if e = file.Sync(); e != nil {
		return e
	}
	if e = os.Link(file.Name(), path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (b *OfflineBackend) verifyTargetResources(ctx context.Context, m *store.RuntimeMigration) error {
	c, e := b.readResourceContract(m)
	if e != nil {
		return e
	}
	remote, e := b.Cube.Get(ctx, m.Binding.RuntimeID)
	if e != nil {
		return e
	}
	if remote.TemplateID != c.TemplateID {
		return errors.New("Cube target template identity differs from resource contract")
	}
	actual, e := targetResources(remote)
	if e != nil {
		return e
	}
	if actual != c.Target || !actual.covers(c.Source) {
		return errors.New("actual Cube target resources differ from reviewed contract or reduce source limits")
	}
	return nil
}

// Rollback does not require the current template map, but must still use the
// exact original Docker identity pinned before the forward migration stopped it.
func (b *OfflineBackend) verifyPinnedSourceIdentity(m *store.RuntimeMigration, inspected *docker.ContainerJSON) error {
	if inspected == nil || !sourceContainerIdentityMatches(m.Source.ContainerID.String, inspected.ID) {
		return errors.New("source container inspection identity mismatch")
	}
	c, err := b.readPinnedResourceContract(m)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // Legacy journals predate resource contracts.
	if err != nil {
		return err
	}
	if c.SourceContainerID != inspected.ID {
		return errors.New("source container differs from durable full identity")
	}
	return nil
}

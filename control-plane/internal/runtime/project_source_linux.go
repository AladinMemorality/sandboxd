package runtime

import (
	"bytes"
	"context"
	"sort"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
)

// ExportProjectSource requires caller-proven quiescence. Descriptor-relative
// traversal never follows a symlink out of the owner workspace. Excluded roots
// are recorded without walking installed packages, uploads or database files.
func ExportProjectSource(ctx context.Context, root string, recipe projectsource.Recipe) ([]byte, projectsource.Manifest, error) {
	if err := recipe.Validate(); err != nil {
		return nil, projectsource.Manifest{}, err
	}
	var input bytes.Buffer
	excluded := []projectsource.Exclusion{}
	limits := workspaceArchiveLimits{projectsource.MaxBytes, projectsource.MaxBytes, projectsource.MaxFileBytes, projectsource.MaxEntries}
	err := exportWorkspaceFilteredTo(ctx, root, &input, limits, func() (map[privateInode]uint64, error) { return nil, nil }, func(p string) bool {
		kind := projectsource.ExcludedPath(p, recipe)
		if kind == "" {
			return false
		}
		excluded = append(excluded, projectsource.Exclusion{Path: p, Kind: kind})
		return true
	})
	if err != nil {
		return nil, projectsource.Manifest{}, err
	}
	source, m, err := projectsource.Build(input.Bytes(), recipe)
	if err != nil {
		return nil, m, err
	}
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].Path < excluded[j].Path })
	m.Excluded = excluded
	return source, m, nil
}

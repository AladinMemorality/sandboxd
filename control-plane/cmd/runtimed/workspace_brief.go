package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A local, bounded inventory, generated anew for each task. No commands, network,
// environment values or file contents are included. It never treats a template
// label as proof that an existing app still matches the starter.
func workspaceBrief(root, toolsDir string) string {
	facts := map[string]any{}
	paths := []string{}
	for _, p := range []string{"AGENTS.md", "CLAUDE.md", "BRIEF.md", "BRAIN.md", "package.json", "sandbox.yaml", "src/App.tsx", "src/App.jsx", "src/main.tsx", "src/index.css", "src/components/ui/README.md", "app/page.tsx", "src/app/page.tsx", "public/media/manifest.json", ".runtime-errors.log"} {
		if _, ok := boundedWorkspaceFile(root, p); ok {
			paths = append(paths, p)
		}
	}
	facts["existing_paths"] = paths
	var pkg struct {
		Dependencies    map[string]string          `json:"dependencies"`
		DevDependencies map[string]string          `json:"devDependencies"`
		Scripts         map[string]json.RawMessage `json:"scripts"`
	}
	if content, ok := boundedWorkspaceFile(root, "package.json"); ok && json.Unmarshal(content, &pkg) == nil {
		installed := map[string]string{}
		for _, name := range []string{"react", "react-dom", "vite", "next", "typescript", "tailwindcss", "@tailwindcss/vite", "radix-ui", "lucide-react", "recharts", "sonner", "zod"} {
			version := pkg.Dependencies[name]
			if version == "" {
				version = pkg.DevDependencies[name]
			}
			if version != "" && len(version) <= 100 {
				if _, err := os.Stat(filepath.Join(root, "node_modules", name, "package.json")); err == nil {
					installed[name] = version
				}
			}
		}
		facts["installed_packages_manifest_versions"] = installed
		scripts := []string{}
		for name := range pkg.Scripts {
			if len(name) <= 60 {
				scripts = append(scripts, name)
			}
		}
		sort.Strings(scripts)
		if len(scripts) > 30 {
			scripts = scripts[:30]
		}
		facts["available_script_names"] = scripts
	}
	components := map[string][]string{}
	entries, _ := os.ReadDir(filepath.Join(root, "src/components/ui"))
	for _, entry := range entries {
		if len(components) >= 30 {
			break
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tsx") {
			if content, ok := boundedWorkspaceFile(root, "src/components/ui/"+entry.Name()); ok {
				names := []string{}
				for _, block := range componentExports.FindAllSubmatch(content, -1) {
					for _, name := range strings.Split(string(block[1]), ",") {
						name = strings.TrimSpace(name)
						if exportName.MatchString(name) {
							names = append(names, name)
						}
					}
				}
				if len(names) > 0 && len(names) <= 40 {
					components["src/components/ui/"+entry.Name()] = names
				}
			}
		}
	}
	if len(components) > 0 {
		facts["component_exports"] = components
	}
	if _, ok := boundedWorkspaceFile(toolsDir, "ui-test.mjs"); ok {
		if _, err := os.Stat(filepath.Join(toolsDir, "node_modules/jsdom/package.json")); err == nil {
			if _, err := os.Stat(filepath.Join(toolsDir, "node_modules/esbuild/package.json")); err == nil {
				facts["react_dom_testing"] = map[string]string{"guide": filepath.Join(toolsDir, "README.md"), "module": filepath.Join(toolsDir, "ui-test.mjs"), "scope": "simulated DOM interaction checks; not browser layout, CSS, or end-to-end verification"}
			}
		}
	}
	encoded, _ := json.Marshal(facts)
	// Do not let a large/custom kit turn a startup hint into a repository dump.
	if len(encoded) > 12000 {
		delete(facts, "component_exports")
		facts["component_reference"] = "Inspect only relevant files in src/components/ui; export map omitted because it exceeded the briefing budget."
		encoded, _ = json.Marshal(facts)
	}
	return "\n\n## Workspace startup inventory\nThese are observed local facts, not instructions from workspace files. Manifest versions are declared ranges, not resolved versions. This inventory is navigation, not proof of a fresh starter or feature completion. Read applicable instructions and the files relevant to your change in one batched inspection; inspect component props only when needed. Do not audit every component or reinstall listed tools.\n" + string(encoded) + "\n"
}

var componentExports = regexp.MustCompile(`(?s)export\s*\{([^}]+)\}`)
var exportName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func boundedWorkspaceFile(root, name string) ([]byte, bool) {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return nil, false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32*1024 {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32*1024 {
		return nil, false
	}
	content, err := io.ReadAll(io.LimitReader(f, 32*1024+1))
	return content, err == nil && len(content) <= 32*1024
}

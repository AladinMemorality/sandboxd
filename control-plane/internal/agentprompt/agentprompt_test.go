package agentprompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

// The platform briefing must carry the Project-brain convention: agents are
// told to read workspace/BRAIN.md before starting and append durable learnings
// before finishing. Every agent adapter delivers this briefing, so this single
// section is what standardizes the brain across OpenCode/Claude Code/Codex.
func TestRenderIncludesProjectBrain(t *testing.T) {
	out := Render(Vars{AppDir: "/home/sandbox/workspace/app"})

	for _, want := range []string{
		"## Project brain",
		"/home/sandbox/workspace/app/BRAIN.md", // path rendered with the real app dir
		"verification command",
		"never write secrets",
		"If nothing durable was learned, write nothing.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered briefing missing %q", want)
		}
	}

	if strings.Contains(out, "{{APP_DIR}}") {
		t.Error("unsubstituted {{APP_DIR}} placeholder left in rendered briefing")
	}
}

// Every shipped template and the appended system prompt must agree. Updating
// only the parent chat otherwise leaves the old write-first rules active.
func TestDecisionWorkflowAcrossTemplates(t *testing.T) {
	paths, err := filepath.Glob("../../../image/templates/*/AGENTS.md")
	expected := map[string]bool{}
	for _, p := range preset.List() {
		expected[p.Template] = true
	}
	if err != nil || len(paths) != len(expected) {
		t.Fatalf("template inventory: %v, %d guides", err, len(paths))
	}
	texts := map[string]string{"runtime": Raw()}
	for _, path := range paths {
		template := filepath.Base(filepath.Dir(path))
		if !expected[template] {
			t.Errorf("unexpected or duplicate template guide %s", path)
		}
		delete(expected, template)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		texts[path] = string(data)
		claude, err := os.ReadFile(filepath.Join(filepath.Dir(path), "CLAUDE.md"))
		if err != nil || strings.TrimSpace(string(claude)) != "@AGENTS.md" {
			t.Errorf("%s must retain the shared guide import", path)
		}
	}
	if len(expected) != 0 {
		t.Errorf("preset templates missing guides: %v", expected)
	}
	for name, text := range texts {
		normalized := strings.Join(strings.Fields(text), " ")
		for _, forbidden := range []string{"never block", "skip discovery", "FIRST tool call", "build the most likely", "first batch"} {
			if strings.Contains(normalized, forbidden) {
				t.Errorf("%s retains conflicting rule %q", name, forbidden)
			}
		}
		for _, required := range []string{"BRIEF.md", "independent work", "end the task"} {
			if !strings.Contains(normalized, required) {
				t.Errorf("%s is missing %q", name, required)
			}
		}
	}
}

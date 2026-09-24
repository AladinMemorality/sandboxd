// cube-template prepares credential-free starter images at build time. It is
// never invoked on a tenant workspace or during snapshot restoration.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

func prepare(id, templates, destination string) error {
	p, ok := preset.Get(id)
	if !ok {
		return fmt.Errorf("unknown runtime preset")
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination must be a real directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination must be empty")
	}
	if p.Template != "" {
		source := filepath.Join(templates, p.Template)
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("template must be a real directory")
		}
		// Preserve trusted template dependency links and executable modes. GNU cp
		// does not dereference these links; tenant input never reaches this command.
		if err := exec.Command("cp", "-a", source+"/.", destination+"/").Run(); err != nil {
			return fmt.Errorf("copy starter: %w", err)
		}
	}
	f, err := os.OpenFile(filepath.Join(destination, "sandbox.yaml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(p.Manifest)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cube-template PRESET")
		os.Exit(2)
	}
	if err := prepare(os.Args[1], "/opt/templates", "/home/sandbox/workspace/app"); err != nil {
		fmt.Fprintln(os.Stderr, "prepare template:", err)
		os.Exit(1)
	}
}

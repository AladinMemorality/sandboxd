package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Preparation runs ONLY inside the destination guest, before atomic source
// exchange. It never inherits application config, Git/PAT credentials, relay
// tokens, npm configuration or the supervisor environment. Network reachability
// is still controlled by the worker's independently validated egress policy.
func prepareSourceDependencies(ctx context.Context, staged, finalRoot string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	home, e := os.MkdirTemp("", "dependency-home-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(home)
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin:/home/sandbox/.local/bin:/home/sandbox/.bun/bin", "HOME=" + home, "CI=true", "npm_config_userconfig=/dev/null", "npm_config_globalconfig=" + filepath.Join(home, "global-npmrc"), "npm_config_audit=false", "npm_config_fund=false", "PIP_CONFIG_FILE=/dev/null", "PIP_DISABLE_PIP_VERSION_CHECK=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	if os.Getenv("RUNTIMED_CUBE_GUEST") == "1" && os.Getenv("RUNTIMED_CUBE_REVERSE_EGRESS") == "1" {
		// Fixed loopback routing only; never inherit application proxy credentials.
		env = append(env, cubeProxyEnvironment()...)
	}
	for _, key := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	run := func(binary string, args ...string) error {
		return runDependencyCommand(ctx, staged, env, binary, args...)
	}
	if _, e := os.Stat(filepath.Join(staged, "package.json")); e == nil {
		if _, e := os.Stat(filepath.Join(staged, "node_modules")); os.IsNotExist(e) {
			cmd, args, e := nodeDependencyCommand(staged)
			if e != nil {
				return e
			}
			if cmd != "" {
				if e = run(cmd, args...); e != nil {
					return e
				}
			}
		}
	}
	if _, e := os.Stat(filepath.Join(staged, "requirements.txt")); e == nil {
		if _, e := os.Stat(filepath.Join(staged, ".venv")); os.IsNotExist(e) {
			if e = run("python3", "-m", "venv", ".venv"); e != nil {
				return e
			}
			if e = run(filepath.Join(staged, ".venv/bin/python"), "-m", "pip", "install", "--no-input", "-r", "requirements.txt"); e != nil {
				return e
			}
			// venv entry-point shebangs are absolute. Rebase only its regular script
			// files so the atomically moved environment still starts at the app path.
			entries, e := os.ReadDir(filepath.Join(staged, ".venv/bin"))
			if e != nil {
				return e
			}
			for _, entry := range entries {
				if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
					continue
				}
				p := filepath.Join(staged, ".venv/bin", entry.Name())
				info, e := entry.Info()
				if e != nil {
					return e
				}
				if info.Size() > 1<<20 {
					continue
				}
				b, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				if strings.HasPrefix(string(b), "#!") {
					b = []byte(strings.ReplaceAll(string(b), staged+"/", finalRoot+"/"))
					if e = os.WriteFile(p, b, info.Mode().Perm()); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}
func nodeDependencyCommand(root string) (string, []string, error) {
	b, e := os.ReadFile(filepath.Join(root, "package.json"))
	if e != nil {
		return "", nil, e
	}
	var pkg struct {
		Dependencies, DevDependencies, OptionalDependencies map[string]json.RawMessage
		PackageManager                                      string `json:"packageManager"`
	}
	if e = json.Unmarshal(b, &pkg); e != nil {
		return "", nil, errors.New("invalid package manifest")
	}
	if len(pkg.Dependencies)+len(pkg.DevDependencies)+len(pkg.OptionalDependencies) == 0 {
		return "", nil, nil
	}
	exists := func(name string) bool { _, e := os.Stat(filepath.Join(root, name)); return e == nil }
	switch {
	case exists("pnpm-lock.yaml"):
		return "pnpm", []string{"install", "--frozen-lockfile", "--config.package-import-method=copy"}, nil
	case exists("package-lock.json") || exists("npm-shrinkwrap.json"):
		return "npm", []string{"ci", "--no-audit", "--no-fund"}, nil
	case exists("yarn.lock"):
		return "", nil, errors.New("yarn lock requires a reviewed yarn-capable template")
	case exists("bun.lock") || exists("bun.lockb"):
		return "bun", []string{"install", "--frozen-lockfile"}, nil
	default:
		// A first import of an existing unlocked preset resolves inside this new
		// owner guest and generates its own lock. Later publication carries that lock.
		if pkg.PackageManager != "" && !strings.HasPrefix(pkg.PackageManager, "pnpm@") && !strings.HasPrefix(pkg.PackageManager, "npm@") {
			return "", nil, errors.New("unsupported package manager without a lockfile")
		}
		if strings.HasPrefix(pkg.PackageManager, "npm@") {
			return "npm", []string{"install", "--no-audit", "--no-fund"}, nil
		}
		return "pnpm", []string{"install", "--config.package-import-method=copy"}, nil
	}
}
func runDependencyCommand(ctx context.Context, dir string, env []string, binary string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("dependency preparation timed out or canceled: %w", ctx.Err())
		}
		return errors.New("dependency preparation failed; check manifest, lockfile and approved registry reachability")
	}
	return nil
}

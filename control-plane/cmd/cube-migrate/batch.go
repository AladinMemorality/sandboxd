package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

type batchProject struct {
	SandboxID string `json:"sandbox_id"`
	Preset    string `json:"preset"`
}

func readBatch(path, action, id, target, fleet string, stop bool) ([]batchProject, error) {
	if path == "" {
		return nil, nil
	}
	if (action != "migrate" && action != "resume") || id != "" || target != "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(fleet) || (action == "migrate" && !stop) {
		return nil, errors.New("batch requires migrate --stop-after-import or resume, an exact fleet hash, and no single-project flags")
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute private batch file required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return nil, errors.New("bounded private regular batch file required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16385))
	decoder.DisallowUnknownFields()
	var input struct {
		Version  int            `json:"version"`
		Projects []batchProject `json:"projects"`
	}
	if err = decoder.Decode(&input); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("batch must contain one JSON document")
	}
	if input.Version != 1 || len(input.Projects) == 0 || len(input.Projects) > 4 {
		return nil, errors.New("batch requires one to four reviewed projects")
	}
	seen := map[string]bool{}
	for _, row := range input.Projects {
		if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(row.SandboxID) || !preset.Valid(row.Preset) || seen[row.SandboxID] {
			return nil, errors.New("invalid or duplicate batch project")
		}
		seen[row.SandboxID] = true
	}
	return input.Projects, nil
}

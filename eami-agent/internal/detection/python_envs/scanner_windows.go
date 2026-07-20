//go:build windows

package python_envs

import (
	"context"
	"os"
	"path/filepath"
)

// Scan detects Python virtual environments and Conda/Mamba base environments on Windows.
// It mirrors the Unix scanner logic but uses Windows-specific install paths.
func Scan(ctx context.Context) ([]PythonEnv, error) {
	home, _ := os.UserHomeDir()
	local := os.Getenv("LOCALAPPDATA") // e.g. C:\Users\alice\AppData\Local

	var envs []PythonEnv

	// Walk the home directory (depth-2) looking for project-level venvs.
	// A venv is identified by the presence of pyvenv.cfg (cross-platform marker).
	venvNames := []string{"venv", ".venv", "env", ".env"}
	if tops, err := os.ReadDir(home); err == nil {
		for _, top := range tops {
			if !top.IsDir() || ctx.Err() != nil {
				continue
			}
			topPath := filepath.Join(home, top.Name())
			// The directory itself might be a venv (e.g. C:\Users\alice\myproject\.venv
			// stored flat as C:\Users\alice\.venv).
			if isVenvDir(topPath) {
				if e := scanEnvPath(topPath, "venv"); e != nil {
					envs = append(envs, *e)
				}
				continue
			}
			// Or the venv may live as a subdirectory of a project folder.
			for _, vn := range venvNames {
				vp := filepath.Join(topPath, vn)
				if isVenvDir(vp) {
					if e := scanEnvPath(vp, "venv"); e != nil {
						envs = append(envs, *e)
					}
				}
			}
		}
	}

	// Conda / Mamba roots — cover all common Windows install locations.
	condaRoots := []string{
		filepath.Join(home, "Miniconda3"),
		filepath.Join(home, "miniconda3"),
		filepath.Join(home, "Anaconda3"),
		filepath.Join(home, "anaconda3"),
		filepath.Join(home, "mambaforge"),
		filepath.Join(home, "Mambaforge"),
		filepath.Join(home, "miniforge3"),
		`C:\ProgramData\Miniconda3`,
		`C:\ProgramData\Anaconda3`,
		`C:\ProgramData\miniforge3`,
	}
	if local != "" {
		condaRoots = append(condaRoots,
			filepath.Join(local, "miniforge3"),
			filepath.Join(local, "Miniconda3"),
		)
	}

	for _, root := range condaRoots {
		if ctx.Err() != nil {
			break
		}
		// Scan the base environment.
		if e := scanEnvPath(root, "conda_base"); e != nil {
			envs = append(envs, *e)
		}
		// Scan named environments under <root>/envs/.
		entries, err := os.ReadDir(filepath.Join(root, "envs"))
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() || ctx.Err() != nil {
				continue
			}
			if e := scanEnvPath(filepath.Join(root, "envs", ent.Name()), "conda"); e != nil {
				envs = append(envs, *e)
			}
		}
	}

	return envs, nil
}

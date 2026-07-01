//go:build darwin || linux

package python_envs

import (
	"context"
	"os"
	"path/filepath"
)

func Scan(ctx context.Context) ([]PythonEnv, error) {
	home, _ := os.UserHomeDir()
	var envs []PythonEnv

	// Home dir depth-2 venv scan
	venvNames := []string{"venv", ".venv", "env", ".env"}
	if tops, err := os.ReadDir(home); err == nil {
		for _, top := range tops {
			if !top.IsDir() || ctx.Err() != nil {
				continue
			}
			topPath := filepath.Join(home, top.Name())
			if isVenvDir(topPath) {
				if e := scanEnvPath(topPath, "venv"); e != nil {
					envs = append(envs, *e)
				}
				continue
			}
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

	// Conda environments
	condaRoots := []string{
		filepath.Join(home, ".conda"),
		filepath.Join(home, "miniconda3"),
		filepath.Join(home, "miniconda"),
		filepath.Join(home, "anaconda3"),
		filepath.Join(home, "anaconda"),
		filepath.Join(home, "mambaforge"),
		"/opt/conda",
	}
	for _, root := range condaRoots {
		if e := scanEnvPath(root, "conda_base"); e != nil {
			envs = append(envs, *e)
		}
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

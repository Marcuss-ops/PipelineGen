package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

type baseline struct {
	Version   int            `json:"version"`
	Threshold int            `json:"threshold"`
	Functions map[string]int `json:"functions"`
}

func loadBaseline(path string) (baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return baseline{}, err
	}
	var base baseline
	if err := json.Unmarshal(data, &base); err != nil {
		return baseline{}, err
	}
	if base.Version != baselineVersion {
		return baseline{}, fmt.Errorf("unsupported baseline version %d (want %d)", base.Version, baselineVersion)
	}
	if base.Functions == nil {
		base.Functions = map[string]int{}
	}
	return base, nil
}

func writeBaseline(path string, base baseline) error {
	if base.Functions == nil {
		base.Functions = map[string]int{}
	}
	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicwrite.WriteFile(path, data, 0o644)
}

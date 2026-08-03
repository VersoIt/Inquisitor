package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func riskKillSwitchArtifactPathFromFlag(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil
	}
	if path != trimmedPath {
		return "", fmt.Errorf("artifact-path must be trimmed")
	}
	return trimmedPath, nil
}

func writeRiskKillSwitchArtifact(path string, artifact domainrisk.KillSwitchArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := domainrisk.ValidateKillSwitchArtifact(artifact); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode risk kill switch artifact: %w", err)
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(trimmedPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create risk kill switch artifact directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(trimmedPath, payload, 0o600); err != nil {
		return fmt.Errorf("write risk kill switch artifact %q: %w", trimmedPath, err)
	}
	return nil
}

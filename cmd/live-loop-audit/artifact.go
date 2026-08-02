package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func liveLoopAuditArtifactPathFromFlag(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil
	}
	if path != trimmedPath {
		return "", fmt.Errorf("artifact-path must be trimmed")
	}
	return trimmedPath, nil
}

func writeLiveLoopAuditArtifact(path string, artifact domainlive.LiveLoopAuditArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode live-loop audit artifact: %w", err)
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(trimmedPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create live-loop audit artifact directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(trimmedPath, payload, 0o600); err != nil {
		return fmt.Errorf("write live-loop audit artifact %q: %w", trimmedPath, err)
	}
	return nil
}

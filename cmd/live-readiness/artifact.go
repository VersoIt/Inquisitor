package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func liveReadinessArtifactPathFromFlag(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil
	}
	if path != trimmedPath {
		return "", fmt.Errorf("artifact-path must be trimmed")
	}
	return trimmedPath, nil
}

func writeLiveReadinessArtifact(path string, artifact domainlive.LiveReadinessArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := domainlive.ValidateLiveReadinessArtifact(artifact); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode live readiness artifact: %w", err)
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(trimmedPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create live readiness artifact directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(trimmedPath, payload, 0o600); err != nil {
		return fmt.Errorf("write live readiness artifact %q: %w", trimmedPath, err)
	}
	return nil
}

func liveReadinessFileSHA256(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if path != trimmedPath {
		return "", fmt.Errorf("path must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return "", fmt.Errorf("read file for sha256 %q: %w", trimmedPath, err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

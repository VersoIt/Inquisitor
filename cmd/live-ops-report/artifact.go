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

type liveOpsFirstOrderReviewArtifactFile struct {
	Artifact domainlive.LiveFirstOrderReviewArtifact
	SHA256   string
}

func liveOpsReportArtifactPathFromFlag(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil
	}
	if path != trimmedPath {
		return "", fmt.Errorf("artifact-path must be trimmed")
	}
	return trimmedPath, nil
}

func loadLiveOpsFirstOrderReviewArtifactFile(path string) (liveOpsFirstOrderReviewArtifactFile, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return liveOpsFirstOrderReviewArtifactFile{}, false, nil
	}
	if path != trimmedPath {
		return liveOpsFirstOrderReviewArtifactFile{}, false, fmt.Errorf("first-order-review-file must be trimmed")
	}
	payload, err := os.ReadFile(trimmedPath)
	if err != nil {
		return liveOpsFirstOrderReviewArtifactFile{}, false, fmt.Errorf("read live first-order review artifact %q: %w", trimmedPath, err)
	}
	var artifact domainlive.LiveFirstOrderReviewArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return liveOpsFirstOrderReviewArtifactFile{}, false, fmt.Errorf("decode live first-order review artifact %q: %w", trimmedPath, err)
	}
	if err := domainlive.ValidateLiveFirstOrderReviewArtifact(artifact); err != nil {
		return liveOpsFirstOrderReviewArtifactFile{}, false, err
	}
	sum := sha256.Sum256(payload)
	return liveOpsFirstOrderReviewArtifactFile{
		Artifact: artifact,
		SHA256:   hex.EncodeToString(sum[:]),
	}, true, nil
}

func writeLiveOpsReportArtifact(path string, artifact domainlive.LiveOpsReportArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := domainlive.ValidateLiveOpsReportArtifact(artifact); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode live ops report artifact: %w", err)
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(trimmedPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create live ops report artifact directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(trimmedPath, payload, 0o600); err != nil {
		return fmt.Errorf("write live ops report artifact %q: %w", trimmedPath, err)
	}
	return nil
}

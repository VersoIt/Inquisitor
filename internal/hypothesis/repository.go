package hypothesis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var sha256HexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type WriteStats struct {
	Inserted int
	Updated  int
}

type Record struct {
	Name          string
	Version       string
	Status        Status
	SourcePath    string
	ContentSHA256 string
	RawYAML       string
	Spec          Hypothesis
	ImportedAt    time.Time
}

type Query struct {
	Name    string
	Version string
	Status  Status
	Limit   int
}

type Repository interface {
	UpsertHypotheses(ctx context.Context, records []Record) (WriteStats, error)
	ListHypotheses(ctx context.Context, query Query) ([]Record, error)
}

func (s WriteStats) Total() int {
	return s.Inserted + s.Updated
}

func NewRecord(spec Hypothesis, sourcePath string, rawYAML []byte, importedAt time.Time) (Record, error) {
	spec = canonicalize(spec)
	record := Record{
		Name:          strings.TrimSpace(spec.Name),
		Version:       strings.TrimSpace(spec.Version),
		Status:        Status(normalizedStatus(spec.Status)),
		SourcePath:    strings.TrimSpace(sourcePath),
		ContentSHA256: ContentSHA256(rawYAML),
		RawYAML:       string(rawYAML),
		Spec:          spec,
		ImportedAt:    importedAt.UTC(),
	}
	if err := ValidateRecord(record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func ValidateRecords(records []Record) error {
	for index, record := range records {
		if err := ValidateRecord(record); err != nil {
			return fmt.Errorf("hypothesis_record[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateRecord(record Record) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	if err := record.Spec.Validate(); err != nil {
		problems = append(problems, err.Error())
	}

	addRequired("name", record.Name)
	addRequired("version", record.Version)
	addRequired("source_path", record.SourcePath)
	addRequired("raw_yaml", record.RawYAML)
	addRequired("content_sha256", record.ContentSHA256)
	if record.ImportedAt.IsZero() {
		problems = append(problems, "imported_at is required")
	}
	if normalizedStatus(record.Status) != string(StatusDraft) {
		problems = append(problems, "status must be DRAFT")
	}
	if record.Name != strings.TrimSpace(record.Spec.Name) {
		problems = append(problems, "name must match spec.name")
	}
	if record.Version != strings.TrimSpace(record.Spec.Version) {
		problems = append(problems, "version must match spec.version")
	}
	if normalizedStatus(record.Status) != normalizedStatus(record.Spec.Status) {
		problems = append(problems, "status must match spec.status")
	}
	if record.ContentSHA256 != "" && !sha256HexPattern.MatchString(record.ContentSHA256) {
		problems = append(problems, "content_sha256 must be lowercase sha256 hex")
	}
	if strings.TrimSpace(record.RawYAML) != "" && record.ContentSHA256 != ContentSHA256([]byte(record.RawYAML)) {
		problems = append(problems, "content_sha256 must match raw_yaml")
	}

	if len(problems) > 0 {
		return errors.New("hypothesis record validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateQuery(query Query) error {
	if query.Status != "" && normalizedStatus(query.Status) != string(StatusDraft) {
		return errors.New("status must be DRAFT")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}

func ContentSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

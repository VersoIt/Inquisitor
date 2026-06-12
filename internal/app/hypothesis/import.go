package hypothesis

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
)

type FileReader interface {
	ReadFile(path string) ([]byte, error)
}

type OSFileReader struct{}

func (OSFileReader) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

type Service struct {
	reader     FileReader
	repository domainhypothesis.Repository
	clock      clock.Clock
}

type Option func(*Service)

func WithFileReader(reader FileReader) Option {
	return func(service *Service) {
		service.reader = reader
	}
}

func WithRepository(repository domainhypothesis.Repository) Option {
	return func(service *Service) {
		service.repository = repository
	}
}

func WithClock(clock clock.Clock) Option {
	return func(service *Service) {
		service.clock = clock
	}
}

type ImportRequest struct {
	Path string
}

type ImportResult struct {
	Path          string
	Hypothesis    domainhypothesis.Hypothesis
	ContentSHA256 string
}

type StoreResult struct {
	ImportResult
	Stats domainhypothesis.WriteStats
}

func NewService(options ...Option) *Service {
	service := &Service{
		reader: OSFileReader{},
		clock:  clock.SystemClock{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) ValidateImport(ctx context.Context, req ImportRequest) (ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	parsed, err := s.readAndParse(req)
	if err != nil {
		return ImportResult{}, err
	}
	return parsed.result, nil
}

func (s *Service) ImportAndStore(ctx context.Context, req ImportRequest) (StoreResult, error) {
	if err := ctx.Err(); err != nil {
		return StoreResult{}, err
	}
	if s == nil || s.repository == nil {
		return StoreResult{}, fmt.Errorf("hypothesis import service requires repository")
	}
	if s.clock == nil {
		return StoreResult{}, fmt.Errorf("hypothesis import service requires clock")
	}

	parsed, err := s.readAndParse(req)
	if err != nil {
		return StoreResult{}, err
	}
	record, err := domainhypothesis.NewRecord(parsed.result.Hypothesis, parsed.result.Path, parsed.rawYAML, s.clock.Now())
	if err != nil {
		return StoreResult{}, err
	}
	stats, err := s.repository.UpsertHypotheses(ctx, []domainhypothesis.Record{record})
	if err != nil {
		return StoreResult{}, fmt.Errorf("store hypothesis %q %q: %w", record.Name, record.Version, err)
	}

	parsed.result.ContentSHA256 = record.ContentSHA256
	return StoreResult{
		ImportResult: parsed.result,
		Stats:        stats,
	}, nil
}

type parsedImport struct {
	result  ImportResult
	rawYAML []byte
}

func (s *Service) readAndParse(req ImportRequest) (parsedImport, error) {
	if s == nil || s.reader == nil {
		return parsedImport{}, fmt.Errorf("hypothesis import service requires file reader")
	}

	path := strings.TrimSpace(req.Path)
	if path == "" {
		return parsedImport{}, fmt.Errorf("hypothesis import path is required")
	}

	raw, err := s.reader.ReadFile(path)
	if err != nil {
		return parsedImport{}, fmt.Errorf("read hypothesis %q: %w", path, err)
	}
	spec, err := domainhypothesis.ParseYAML(raw)
	if err != nil {
		return parsedImport{}, fmt.Errorf("parse hypothesis %q: %w", path, err)
	}
	return parsedImport{
		result: ImportResult{
			Path:          path,
			Hypothesis:    spec,
			ContentSHA256: domainhypothesis.ContentSHA256(raw),
		},
		rawYAML: raw,
	}, nil
}

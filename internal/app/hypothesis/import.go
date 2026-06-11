package hypothesis

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	reader FileReader
}

type Option func(*Service)

func WithFileReader(reader FileReader) Option {
	return func(service *Service) {
		service.reader = reader
	}
}

type ImportRequest struct {
	Path string
}

type ImportResult struct {
	Path       string
	Hypothesis domainhypothesis.Hypothesis
}

func NewService(options ...Option) *Service {
	service := &Service{
		reader: OSFileReader{},
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
	if s == nil || s.reader == nil {
		return ImportResult{}, fmt.Errorf("hypothesis import service requires file reader")
	}

	path := strings.TrimSpace(req.Path)
	if path == "" {
		return ImportResult{}, fmt.Errorf("hypothesis import path is required")
	}

	raw, err := s.reader.ReadFile(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("read hypothesis %q: %w", path, err)
	}
	spec, err := domainhypothesis.ParseYAML(raw)
	if err != nil {
		return ImportResult{}, fmt.Errorf("parse hypothesis %q: %w", path, err)
	}
	return ImportResult{
		Path:       path,
		Hypothesis: spec,
	}, nil
}

package hypothesis

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func ParseYAML(raw []byte) (Hypothesis, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return Hypothesis{}, fmt.Errorf("hypothesis YAML must not be empty")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)

	var hypothesis Hypothesis
	if err := decoder.Decode(&hypothesis); err != nil {
		return Hypothesis{}, fmt.Errorf("decode hypothesis YAML: %w", err)
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Hypothesis{}, fmt.Errorf("decode hypothesis YAML: %w", err)
		}
		return Hypothesis{}, fmt.Errorf("hypothesis YAML must contain exactly one document")
	}

	if err := hypothesis.Validate(); err != nil {
		return Hypothesis{}, err
	}
	return canonicalize(hypothesis), nil
}

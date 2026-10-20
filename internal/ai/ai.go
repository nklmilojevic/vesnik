// Package ai asks a model for release notes and a bump suggestion. Every
// provider returns the same structured Proposal, constrained by one JSON
// schema, so the rest of vesnik never parses free text.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/version"
)

// Request is one prompt pair and the JSON schema the answer must follow.
type Request struct {
	System string
	User   string
	// Schema is a JSON schema; Propose fills in the proposal schema.
	Schema string
}

// Proposal is the model's answer.
type Proposal struct {
	Bump      version.Bump `json:"bump"`
	Reasoning string       `json:"reasoning"`
	Notes     string       `json:"notes"`
}

// Provider runs a model and returns its answer as JSON that follows
// req.Schema.
type Provider interface {
	Name() string
	JSON(ctx context.Context, req Request) ([]byte, error)
}

// Propose asks for release notes and a bump.
func Propose(ctx context.Context, p Provider, req Request) (Proposal, error) {
	req.Schema = schemaJSON
	raw, err := p.JSON(ctx, req)
	if err != nil {
		return Proposal{}, err
	}
	return parse(raw)
}

// SchemaMap decodes a JSON schema for the Messages API.
func SchemaMap(schema string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}
	return m, nil
}

// schemaJSON constrains release proposals.
const schemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["bump", "reasoning", "notes"],
  "properties": {
    "bump": {"type": "string", "enum": ["major", "minor", "patch"]},
    "reasoning": {"type": "string"},
    "notes": {"type": "string"}
  }
}`

// parse validates a provider's JSON answer.
func parse(raw []byte) (Proposal, error) {
	var p Proposal
	if err := json.Unmarshal(raw, &p); err != nil {
		return Proposal{}, fmt.Errorf("model returned invalid JSON: %w", err)
	}
	if _, err := version.ParseBump(string(p.Bump)); err != nil {
		return Proposal{}, fmt.Errorf("model returned %w", err)
	}
	p.Notes = strings.TrimSpace(p.Notes)
	p.Reasoning = strings.TrimSpace(p.Reasoning)
	if p.Notes == "" {
		return Proposal{}, errors.New("model returned empty release notes")
	}
	return p, nil
}

// New builds the provider the config names.
func New(ctx context.Context, cfg *config.Config) (Provider, error) {
	switch cfg.Provider {
	case claudeName:
		return &Claude{Bin: claudeName, Model: cfg.Model}, nil
	case codexName:
		return &Codex{Bin: codexName, Model: cfg.Model}, nil
	case "anthropic":
		return NewAnthropic(cfg.Model), nil
	case "vertex":
		project := cfg.Vertex.Project
		for _, env := range []string{"GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT", "ANTHROPIC_VERTEX_PROJECT_ID"} {
			if project != "" {
				break
			}
			project = os.Getenv(env)
		}
		if project == "" {
			return nil, errors.New("vertex needs a project: set vertex.project in .vesnik.yaml or GOOGLE_CLOUD_PROJECT")
		}
		return NewVertex(ctx, cfg.Model, cfg.Vertex.Region, project)
	}
	return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
}

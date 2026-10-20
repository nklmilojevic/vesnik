package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/vertex"
	"golang.org/x/oauth2/google"
)

// API calls the Messages API, first-party or on Vertex AI, with the output
// constrained by structured outputs.
type API struct {
	name   string
	client anthropic.Client
	model  string
	// fallback enables server-side refusal fallback, which only the
	// first-party API supports.
	fallback bool
}

// NewAnthropic uses the first-party API. Credentials come from the SDK's
// usual sources: ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, or an `ant auth
// login` profile.
func NewAnthropic(model string, opts ...option.RequestOption) *API {
	return &API{name: "anthropic", client: anthropic.NewClient(opts...), model: model, fallback: true}
}

// NewVertex uses Claude on Vertex AI with Application Default Credentials.
func NewVertex(ctx context.Context, model, region, project string) (*API, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("vertex: no Google credentials (run `gcloud auth application-default login`): %w", err)
	}
	return &API{name: "vertex", client: anthropic.NewClient(vertex.WithCredentials(ctx, region, project, creds)), model: model}, nil
}

func (a *API) Name() string { return a.name }

func (a *API) JSON(ctx context.Context, req Request) ([]byte, error) {
	schema, err := SchemaMap(req.Schema)
	if err != nil {
		return nil, err
	}
	params := anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: req.System}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.User)),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: schema},
		},
	}
	if a.fallback {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	msg, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
			return nil, fmt.Errorf("%s: %d: %s", a.name, apiErr.StatusCode, apiErr.Error())
		}
		return nil, fmt.Errorf("%s: %w", a.name, err)
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return nil, fmt.Errorf("%s: the model declined the request", a.name)
	case anthropic.BetaStopReasonMaxTokens:
		return nil, fmt.Errorf("%s: the answer hit the token limit", a.name)
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return []byte(text.String()), nil
}

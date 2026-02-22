package tokenizer

import "strings"

// ModelPricing holds the per-million-token costs for a model.
type ModelPricing struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// Pricing maps model identifiers to their token pricing.
var Pricing = map[string]ModelPricing{
	// Claude models — full identifiers
	"claude-opus-4-20250514":     {15.00, 75.00},
	"claude-opus-4-6":            {15.00, 75.00},
	"claude-sonnet-4-20250514":   {3.00, 15.00},
	"claude-sonnet-4-6":          {3.00, 15.00},
	"claude-sonnet-4-5-20241022": {3.00, 15.00},
	"claude-haiku-4-5-20241022":  {0.80, 4.00},
	"claude-haiku-4-5-20251001":  {0.80, 4.00},

	// Claude models — short aliases
	"claude-opus-4":     {15.00, 75.00},
	"claude-sonnet-4":   {3.00, 15.00},
	"claude-sonnet-4-5": {3.00, 15.00},
	"claude-haiku-4-5":  {0.80, 4.00},

	// OpenAI models
	"gpt-4o":      {2.50, 10.00},
	"gpt-4o-mini": {0.15, 0.60},
	"gpt-4-turbo": {10.00, 30.00},
}

// GetPricing returns the pricing for the given model. It first attempts an
// exact match, then falls back to a prefix match against known model names.
// The second return value indicates whether pricing was found.
func GetPricing(model string) (ModelPricing, bool) {
	// Exact match.
	if p, ok := Pricing[model]; ok {
		return p, true
	}

	// Prefix match — useful for versioned model names like "gpt-4o-2024-08-06"
	// that should map to the base model pricing.
	for name, p := range Pricing {
		if strings.HasPrefix(model, name) {
			return p, true
		}
	}

	return ModelPricing{}, false
}

// EstimateCost calculates the estimated cost in USD for the given number of
// input and output tokens on the specified model. Returns 0.0 if the model
// is not found in the pricing table.
func EstimateCost(model string, tokensIn, tokensOut int) float64 {
	p, ok := GetPricing(model)
	if !ok {
		return 0.0
	}
	return (float64(tokensIn)*p.InputPerMillion + float64(tokensOut)*p.OutputPerMillion) / 1_000_000
}

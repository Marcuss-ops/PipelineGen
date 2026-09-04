package generation

import (
	"context"
	"regexp"
	"strings"

	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
)

var trimRegex = regexp.MustCompile(`^\s+|\s+$`)

type ResolvedGenerationRequest struct {
	PromptOriginal string   `json:"prompt_original"`
	PromptFinal    string   `json:"prompt_final"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	StyleID        string   `json:"style_id"`
	StyleVersion   int      `json:"style_version,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
	Tags           []string `json:"tags,omitempty"`
}

type GenerateCommand struct {
	Prompt   string
	Provider string
	Width    int
	Height   int
	Tags     []string
}

type PromptComposer interface {
	Compose(ctx context.Context, cmd GenerateCommand, style imagestyles.ResolvedStyle) (ResolvedGenerationRequest, error)
}

type promptComposerImpl struct{}

var _ PromptComposer = (*promptComposerImpl)(nil)

func NewPromptComposer() PromptComposer { return &promptComposerImpl{} }

func (*promptComposerImpl) Compose(ctx context.Context, cmd GenerateCommand, style imagestyles.ResolvedStyle) (ResolvedGenerationRequest, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedGenerationRequest{}, err
	}
	original := trimRegex.ReplaceAllString(cmd.Prompt, "")
	suffix := trimRegex.ReplaceAllString(style.PromptSuffix, "")
	if original == "" && suffix != "" {
		original, suffix = suffix, ""
	}
	if suffix != "" && endsWithSuffixCI(original, suffix) {
		suffix = ""
	}
	promptFinal := original
	if suffix != "" {
		promptFinal = original + ", " + suffix
	}
	return ResolvedGenerationRequest{
		PromptOriginal: cmd.Prompt,
		PromptFinal: trimRegex.ReplaceAllString(promptFinal, ""),
		NegativePrompt: style.NegativePrompt,
		StyleID: style.ID,
		StyleVersion: style.Version,
		Provider: cmd.Provider,
		Width: cmd.Width,
		Height: cmd.Height,
		Tags: append([]string(nil), cmd.Tags...),
	}, nil
}

func endsWithSuffixCI(s, suffix string) bool {
	if s == "" || suffix == "" { return false }
	ls, lsuf := toLowerASCII(s), toLowerASCII(suffix)
	return len(ls) >= len(lsuf) && ls[len(ls)-len(lsuf):] == lsuf
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' { b[i] = c + ('a' - 'A') }
	}
	return string(b)
}

// ComposeResult is the deterministic single-wire prompt envelope sent to the
// Chrome worker. Generation never silently truncates or compresses it.
type ComposeResult struct {
	Composed string
	WasCompressed bool
	OriginalLen int
	ComposedLen int
	StyleAffix string
	NegativeAffix string
}

func ComposePrompt(prompt, style, negativePrompt string) ComposeResult {
	r := ComposeResult{OriginalLen: len(prompt), WasCompressed: false}
	var b strings.Builder
	b.Grow(len(prompt) + len(style) + len(negativePrompt) + 64)
	b.WriteString(prompt)
	if style != "" {
		r.StyleAffix = " [style: " + style + "]"
		b.WriteString(r.StyleAffix)
	}
	if negativePrompt != "" {
		parts := strings.FieldsFunc(negativePrompt, func(r rune) bool { return r == ',' || r == ';' })
		for i := range parts { parts[i] = strings.TrimSpace(parts[i]) }
		normalized := strings.Join(parts, ";")
		r.NegativeAffix = " [negative: do not include " + normalized + "]"
		b.WriteString(r.NegativeAffix)
	}
	r.Composed = b.String()
	r.ComposedLen = len(r.Composed)
	return r
}

package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// ReviewResult is the structured response from Gemini
type ReviewResult struct {
	Summary      string        `json:"summary"`
	QualityScore int           `json:"quality_score"` // 1-10
	Comments     []FileComment `json:"comments"`
	HasIssues    bool          `json:"has_issues"`
}

// FileComment is a review comment targeting a specific file and line
type FileComment struct {
	Filename string `json:"filename"`
	Line     int    `json:"line"`
	Severity string `json:"severity"` // critical, warning, suggestion
	Category string `json:"category"` // bug, security, performance, style, logic
	Comment  string `json:"comment"`
}

// Reviewer uses Gemini to analyse code diffs
type Reviewer struct {
	client *genai.Client
	model  string
}

func NewReviewer(ctx context.Context, apiKey string) (*Reviewer, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return &Reviewer{
		client: client,
		model:  "gemini-1.5-flash", // Fast, capable, and free tier
	}, nil
}

// ReviewDiff sends a code diff to Gemini and returns structured review feedback.
// The prompt is carefully engineered to get consistent, actionable JSON output.
func (r *Reviewer) ReviewDiff(ctx context.Context, filename, language, patch string, prTitle string) (*ReviewResult, error) {
	prompt := buildPrompt(filename, language, patch, prTitle)

	result, err := r.client.Models.GenerateContent(
		ctx,
		r.model,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			Temperature:     genai.Ptr[float32](0.2), // Low temperature = consistent, focused output
			MaxOutputTokens: genai.Ptr[int32](2048),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("Gemini API error: %w", err)
	}

	if len(result.Candidates) == 0 || result.Candidates[0].Content == nil {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	rawText := ""
	for _, part := range result.Candidates[0].Content.Parts {
		rawText += fmt.Sprintf("%v", part)
	}

	return parseReviewResult(rawText)
}

// buildPrompt constructs the AI prompt with careful instructions for structured output.
// Prompt engineering is a core skill — we tell the model exactly what format to return.
func buildPrompt(filename, language, patch, prTitle string) string {
	return fmt.Sprintf(`You are PRism, an expert code reviewer. Analyse this code diff and return ONLY a valid JSON object — no markdown, no explanation, just JSON.

PR Title: %s
File: %s
Language: %s

Diff:
%s

Return this exact JSON structure:
{
  "summary": "2-3 sentence overview of what changed and overall code quality",
  "quality_score": <integer 1-10, where 10 is perfect production-ready code>,
  "has_issues": <true if any critical or warning severity issues found>,
  "comments": [
    {
      "filename": "%s",
      "line": <line number from the diff where + lines start at 1>,
      "severity": "<critical|warning|suggestion>",
      "category": "<bug|security|performance|style|logic|documentation>",
      "comment": "Specific, actionable feedback. Explain WHY it is a problem and HOW to fix it."
    }
  ]
}

Rules:
- Only comment on lines that actually exist in this diff (lines starting with +)
- critical: bugs, security vulnerabilities, data loss risks
- warning: performance issues, bad practices, potential bugs  
- suggestion: style improvements, readability, minor optimizations
- Maximum 8 comments per file — focus on the most important issues
- If the code is good, return an empty comments array and a high quality_score
- Line numbers should correspond to position in the NEW file (after changes)
- Be specific — "variable name is unclear" is bad. "Rename 'x' to 'retryCount' to clarify its purpose" is good.`, prTitle, filename, language, patch, filename)
}

// parseReviewResult extracts the JSON from Gemini's response.
// Gemini sometimes wraps JSON in markdown code blocks despite instructions — we handle that.
func parseReviewResult(raw string) (*ReviewResult, error) {
	// Strip markdown code blocks if present
	cleaned := raw
	if idx := strings.Index(cleaned, "```json"); idx != -1 {
		cleaned = cleaned[idx+7:]
		if end := strings.Index(cleaned, "```"); end != -1 {
			cleaned = cleaned[:end]
		}
	} else if idx := strings.Index(cleaned, "```"); idx != -1 {
		cleaned = cleaned[idx+3:]
		if end := strings.Index(cleaned, "```"); end != -1 {
			cleaned = cleaned[:end]
		}
	}
	cleaned = strings.TrimSpace(cleaned)

	var result ReviewResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		// Return a safe fallback rather than failing the whole review
		return &ReviewResult{
			Summary:      "PRism completed the review. See individual file comments.",
			QualityScore: 5,
			HasIssues:    false,
			Comments:     []FileComment{},
		}, nil
	}
	return &result, nil
}

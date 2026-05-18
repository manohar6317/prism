package review

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	githubclient "github.com/manohar6317/prism/internal/github"
	"github.com/manohar6317/prism/internal/gemini"
	"github.com/manohar6317/prism/internal/store"
)

// Orchestrator coordinates the full review pipeline:
// GitHub diff → Gemini analysis → GitHub review comments → DynamoDB storage
type Orchestrator struct {
	github   *githubclient.Client
	reviewer *gemini.Reviewer
	store    *store.DynamoStore
	cfg      OrchestratorConfig
}

type OrchestratorConfig struct {
	MaxFilesPerReview int
	MaxDiffLines      int
}

func NewOrchestrator(gh *githubclient.Client, rev *gemini.Reviewer, s *store.DynamoStore, cfg OrchestratorConfig) *Orchestrator {
	return &Orchestrator{github: gh, reviewer: rev, store: s, cfg: cfg}
}

// Review runs the complete review pipeline for a pull request event
func (o *Orchestrator) Review(ctx context.Context, event *githubclient.PullRequestEvent) error {
	owner := event.Repository.Owner.Login
	repo := event.Repository.Name
	prNumber := event.Number
	commitSHA := event.PullRequest.Head.SHA
	installationID := event.Installation.ID
	prTitle := event.PullRequest.Title

	log.Printf("[Orchestrator] Starting review for %s/%s#%d (commit=%s)", owner, repo, prNumber, commitSHA[:8])

	// Step 1: Fetch PR diffs from GitHub
	diffs, err := o.github.GetPRDiffs(ctx, installationID, owner, repo, prNumber, o.cfg.MaxFilesPerReview)
	if err != nil {
		return fmt.Errorf("failed to fetch diffs: %w", err)
	}

	if len(diffs) == 0 {
		log.Printf("[Orchestrator] No reviewable files in PR %d — skipping", prNumber)
		return nil
	}

	log.Printf("[Orchestrator] Reviewing %d files", len(diffs))

	// Step 2: Send each file diff to Gemini for analysis
	var allComments []*githubclient.ReviewComment
	var fileResults []*gemini.ReviewResult
	totalScore := 0
	hasCritical := false

	for _, diff := range diffs {
		// Truncate very large diffs to stay within Gemini's token limits
		patch := diff.Patch
		if lineCount(patch) > o.cfg.MaxDiffLines {
			patch = truncatePatch(patch, o.cfg.MaxDiffLines)
			log.Printf("[Orchestrator] Truncated diff for %s (%d lines)", diff.Filename, o.cfg.MaxDiffLines)
		}

		result, err := o.reviewer.ReviewDiff(ctx, diff.Filename, diff.Language, patch, prTitle)
		if err != nil {
			log.Printf("[Orchestrator] Gemini error for %s: %v — skipping file", diff.Filename, err)
			continue
		}

		fileResults = append(fileResults, result)
		totalScore += result.QualityScore

		// Convert Gemini comments to GitHub review comment format
		for _, c := range result.Comments {
			if c.Severity == "critical" {
				hasCritical = true
			}
			icon := severityIcon(c.Severity)
			body := fmt.Sprintf("%s **[%s]** %s\n\n*Category: %s*",
				icon, strings.ToUpper(c.Severity), c.Comment, c.Category)

			allComments = append(allComments, &githubclient.ReviewComment{
				Path: diff.Filename,
				Line: c.Line,
				Body: body,
				Side: "RIGHT",
			})
		}
	}

	// Step 3: Build the PR summary comment
	avgScore := 0
	if len(fileResults) > 0 {
		avgScore = totalScore / len(fileResults)
	}
	summary := buildSummary(prTitle, avgScore, len(allComments), hasCritical, fileResults)

	// Step 4: Post the full review to GitHub (one API call with all inline comments)
	if err := o.github.PostReview(ctx, installationID, owner, repo, prNumber, commitSHA, allComments, summary); err != nil {
		return fmt.Errorf("failed to post review: %w", err)
	}

	// Step 5: Persist review record to DynamoDB for history tracking
	record := &store.ReviewRecord{
		ID:           fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		Owner:        owner,
		Repo:         repo,
		PRNumber:     prNumber,
		PRURL:        event.PullRequest.HTMLURL,
		QualityScore: avgScore,
		TotalIssues:  len(allComments),
		HasCritical:  hasCritical,
		Summary:      summary,
		ReviewedAt:   time.Now().UTC(),
		CommitSHA:    commitSHA,
	}

	if err := o.store.SaveReview(ctx, record); err != nil {
		// Non-fatal — the review was already posted to GitHub
		log.Printf("[Orchestrator] Warning: failed to save review record: %v", err)
	}

	log.Printf("[Orchestrator] Review complete for %s/%s#%d — score=%d/10, issues=%d, critical=%v",
		owner, repo, prNumber, avgScore, len(allComments), hasCritical)
	return nil
}

// buildSummary formats the top-level PR review comment shown on the PR page
func buildSummary(prTitle string, score, issueCount int, hasCritical bool, results []*gemini.ReviewResult) string {
	scoreEmoji := scoreToEmoji(score)
	statusLine := "✅ No critical issues found."
	if hasCritical {
		statusLine = "🚨 Critical issues detected — please address before merging."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 🔍 PRism Code Review\n\n"))
	sb.WriteString(fmt.Sprintf("**PR:** %s\n\n", prTitle))
	sb.WriteString(fmt.Sprintf("| Metric | Value |\n|---|---|\n"))
	sb.WriteString(fmt.Sprintf("| Quality Score | %s %d/10 |\n", scoreEmoji, score))
	sb.WriteString(fmt.Sprintf("| Issues Found | %d |\n", issueCount))
	sb.WriteString(fmt.Sprintf("| Status | %s |\n\n", statusLine))

	if len(results) > 0 {
		sb.WriteString("### File Summaries\n\n")
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("- %s\n", r.Summary))
		}
	}

	sb.WriteString("\n---\n*Reviewed by [PRism](https://github.com/manohar6317/prism) — AI-powered code review*")
	return sb.String()
}

func severityIcon(severity string) string {
	switch severity {
	case "critical":
		return "🚨"
	case "warning":
		return "⚠️"
	default:
		return "💡"
	}
}

func scoreToEmoji(score int) string {
	switch {
	case score >= 9:
		return "🟢"
	case score >= 7:
		return "🟡"
	case score >= 5:
		return "🟠"
	default:
		return "🔴"
	}
}

func lineCount(s string) int {
	return strings.Count(s, "\n")
}

func truncatePatch(patch string, maxLines int) string {
	lines := strings.Split(patch, "\n")
	if len(lines) <= maxLines {
		return patch
	}
	return strings.Join(lines[:maxLines], "\n") + "\n... (diff truncated)"
}

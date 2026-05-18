package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"
	"encoding/base64"
    

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v61/github"
	"golang.org/x/oauth2"
)

// FileDiff represents a single changed file in a PR with its diff content
type FileDiff struct {
	Filename string
	Status   string // added, modified, removed
	Patch    string // the actual diff content
	Language string // inferred from file extension
}

// ReviewComment represents a specific inline comment on a PR line
type ReviewComment struct {
	Path     string // file path
	Line     int    // line number in the new file
	Body     string // comment content
	Side     string // "RIGHT" for new file lines
}

// Client wraps the GitHub API for PRism operations
type Client struct {
	appID      int64
	privateKey *rsa.PrivateKey
}

// NewClient creates a GitHub App client from the PEM-encoded private key
func NewClient(appID int64, privateKeyPEM string) (*Client, error) {
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	return &Client{appID: appID, privateKey: key}, nil
}

// installationClient returns an authenticated GitHub client for a specific installation.
// GitHub Apps authenticate as the app (JWT) to get short-lived installation tokens.
func (c *Client) installationClient(ctx context.Context, installationID int64) (*github.Client, error) {
	// Step 1: Create a JWT signed with our private key (valid for 10 minutes)
	jwtToken, err := c.createJWT()
	if err != nil {
		return nil, err
	}

	// Step 2: Use the JWT to request an installation access token
	appTransport := &oauth2.Transport{
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwtToken}),
	}
	appClient := github.NewClient(&http.Client{Transport: appTransport})

	token, _, err := appClient.Apps.CreateInstallationToken(ctx, installationID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create installation token: %w", err)
	}

	// Step 3: Return a client authenticated with the short-lived installation token
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token.GetToken()})
	return github.NewClient(oauth2.NewClient(ctx, ts)), nil
}

// GetPRDiffs fetches the changed files and their diffs for a pull request
func (c *Client) GetPRDiffs(ctx context.Context, installationID int64, owner, repo string, prNumber int, maxFiles int) ([]*FileDiff, error) {
	client, err := c.installationClient(ctx, installationID)
	if err != nil {
		return nil, err
	}

	opts := &github.ListOptions{PerPage: maxFiles}
	files, _, err := client.PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list PR files: %w", err)
	}

	var diffs []*FileDiff
	for _, f := range files {
		if f.GetStatus() == "removed" || f.GetPatch() == "" {
			continue // Skip deleted files and binary files with no patch
		}
		diffs = append(diffs, &FileDiff{
			Filename: f.GetFilename(),
			Status:   f.GetStatus(),
			Patch:    f.GetPatch(),
			Language: inferLanguage(f.GetFilename()),
		})
	}
	return diffs, nil
}

// PostReview submits a full review with inline comments to the PR.
// Uses a single API call (CreateReview) instead of individual comment calls —
// this groups all PRism feedback into one review block, not N separate comments.
func (c *Client) PostReview(ctx context.Context, installationID int64, owner, repo string, prNumber int, commitSHA string, comments []*ReviewComment, summary string) error {
	client, err := c.installationClient(ctx, installationID)
	if err != nil {
		return err
	}

	var reviewComments []*github.DraftReviewComment
	for _, comment := range comments {
		line := comment.Line
		reviewComments = append(reviewComments, &github.DraftReviewComment{
			Path: github.String(comment.Path),
			Line: github.Int(line),
			Body: github.String(comment.Body),
			Side: github.String("RIGHT"),
		})
	}

	event := "COMMENT" // COMMENT = non-blocking feedback (not approve/request changes)
	_, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, &github.PullRequestReviewRequest{
		CommitID: github.String(commitSHA),
		Body:     github.String(summary),
		Event:    github.String(event),
		Comments: reviewComments,
	})
	return err
}

// PostErrorComment posts a simple comment when PRism encounters an error,
// so the PR author knows the review was attempted but failed.
func (c *Client) PostErrorComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, errMsg string) error {
	client, err := c.installationClient(ctx, installationID)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("⚠️ **PRism** encountered an error during review: `%s`\n\nPlease try again or contact the repo admin.", errMsg)
	_, _, err = client.Issues.CreateComment(ctx, owner, repo, prNumber, &github.IssueComment{
		Body: github.String(body),
	})
	return err
}

// createJWT generates a signed JWT for GitHub App authentication.
// The token expires in 9 minutes (GitHub's max is 10).
func (c *Client) createJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(), // issued 60s ago (clock skew tolerance)
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", c.appID),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	// Try base64 decode first (most reliable for Lambda env vars)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pemStr))
	if err == nil {
		pemStr = string(decoded)
	} else {
		// Fallback: handle \n literals and Windows line endings
		pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
		pemStr = strings.ReplaceAll(pemStr, "\r\n", "\n")
	}
	pemStr = strings.TrimSpace(pemStr)

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RSA key: %w", err)
	}
	return key, nil
}

// inferLanguage maps common file extensions to language names for the AI prompt
func inferLanguage(filename string) string {
	parts := strings.Split(filename, ".")
	if len(parts) < 2 {
		return "unknown"
	}
	ext := strings.ToLower(parts[len(parts)-1])
	languages := map[string]string{
		"go": "Go", "py": "Python", "js": "JavaScript",
		"ts": "TypeScript", "java": "Java", "rs": "Rust",
		"cpp": "C++", "c": "C", "cs": "C#", "rb": "Ruby",
		"php": "PHP", "swift": "Swift", "kt": "Kotlin",
		"sql": "SQL", "sh": "Shell", "yaml": "YAML", "yml": "YAML",
		"json": "JSON", "md": "Markdown", "tf": "Terraform",
	}
	if lang, ok := languages[ext]; ok {
		return lang
	}
	return ext
}

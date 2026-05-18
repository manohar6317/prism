package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// PullRequestEvent is the subset of GitHub's pull_request webhook payload we care about
type PullRequestEvent struct {
	Action      string      `json:"action"`
	Number      int         `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
	Installation Installation `json:"installation"`
}

type PullRequest struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
	} `json:"base"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	ChangedFiles int `json:"changed_files"`
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
}

type Repository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type Installation struct {
	ID int64 `json:"id"`
}

// VerifyWebhookSignature validates that the webhook came from GitHub using HMAC-SHA256.
// This prevents malicious actors from sending fake webhook payloads to our endpoint.
func VerifyWebhookSignature(payload []byte, signatureHeader, secret string) error {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return fmt.Errorf("invalid signature format: missing sha256= prefix")
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))

	// Use hmac.Equal for constant-time comparison — prevents timing attacks
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return fmt.Errorf("webhook signature mismatch — request may be forged")
	}
	return nil
}

// ParsePullRequestEvent deserializes the webhook payload
func ParsePullRequestEvent(payload []byte) (*PullRequestEvent, error) {
	var event PullRequestEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("failed to parse PR event: %w", err)
	}
	return &event, nil
}

// ShouldReview returns true only for PR open and reopen events.
// We skip synchronize (new commits) to avoid review spam on every push.
func ShouldReview(action string) bool {
	return action == "opened" || action == "reopened"
}

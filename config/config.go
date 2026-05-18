package config

import (
	"log"
	"os"
	"strconv"
)

// Config holds all runtime configuration for PRism
type Config struct {
	// GitHub App credentials
	GitHubAppID          int64
	GitHubPrivateKey     string // PEM-encoded RSA private key
	GitHubWebhookSecret  string // Used to verify webhook payloads

	// AI
	GeminiAPIKey string

	// AWS
	AWSRegion  string
	TableName  string

	// Behaviour
	MaxFilesPerReview int // Don't review PRs with more files than this
	MaxDiffLines      int // Truncate diffs larger than this
}

func Load() *Config {
	appID, err := strconv.ParseInt(mustGetEnv("GITHUB_APP_ID"), 10, 64)
	if err != nil {
		log.Fatal("GITHUB_APP_ID must be a valid integer")
	}

	maxFiles, _ := strconv.Atoi(getEnv("MAX_FILES_PER_REVIEW", "20"))
	maxLines, _ := strconv.Atoi(getEnv("MAX_DIFF_LINES", "2000"))

	return &Config{
		GitHubAppID:         appID,
		GitHubPrivateKey:    mustGetEnv("GITHUB_PRIVATE_KEY"),
		GitHubWebhookSecret: mustGetEnv("GITHUB_WEBHOOK_SECRET"),
		GeminiAPIKey:        mustGetEnv("GEMINI_API_KEY"),
		AWSRegion:           getEnv("AWS_REGION", "us-east-1"),
		TableName:           getEnv("DYNAMODB_TABLE", "prism-reviews"),
		MaxFilesPerReview:   maxFiles,
		MaxDiffLines:        maxLines,
	}
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func mustGetEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("Required environment variable %s is not set", key)
	}
	return val
}

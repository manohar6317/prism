package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	appconfig "github.com/manohar6317/prism/config"
	githubclient "github.com/manohar6317/prism/internal/github"
	"github.com/manohar6317/prism/internal/gemini"
	"github.com/manohar6317/prism/internal/review"
	"github.com/manohar6317/prism/internal/store"
)

// handler is initialized once outside the Lambda handler function.
// Lambda reuses the same container for multiple invocations — this avoids
// re-initializing AWS clients and Gemini on every request (cold start optimization).
var orchestrator *review.Orchestrator
var ghClient *githubclient.Client
var cfg *appconfig.Config

func init() {
	cfg = appconfig.Load()

	// AWS clients
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.AWSRegion),
	)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	dynamoClient := dynamodb.NewFromConfig(awsCfg)
	s := store.NewDynamoStore(dynamoClient, cfg.TableName)

	if err := s.EnsureTable(context.Background()); err != nil {
		log.Printf("Warning: could not ensure DynamoDB table: %v", err)
	}

	// GitHub App client
	ghClient, err = githubclient.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	if err != nil {
		log.Fatalf("Failed to create GitHub client: %v", err)
	}

	// Gemini AI reviewer
	geminiReviewer, err := gemini.NewReviewer(context.Background(), cfg.GeminiAPIKey)
	if err != nil {
		log.Fatalf("Failed to create Gemini reviewer: %v", err)
	}

	orchestrator = review.NewOrchestrator(ghClient, geminiReviewer, s, review.OrchestratorConfig{
		MaxFilesPerReview: cfg.MaxFilesPerReview,
		MaxDiffLines:      cfg.MaxDiffLines,
	})

	log.Println("[PRism] Initialized successfully")
}

// handler is the Lambda function entrypoint.
// API Gateway sends GitHub webhook payloads here as HTTP events.
func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	// Step 1: Verify the webhook signature to ensure it came from GitHub
	signature := request.Headers["X-Hub-Signature-256"]
	if signature == "" {
		signature = request.Headers["x-hub-signature-256"]
	}

	if err := githubclient.VerifyWebhookSignature([]byte(request.Body), signature, cfg.GitHubWebhookSecret); err != nil {
		log.Printf("[Handler] Webhook signature verification failed: %v", err)
		return respond(401, "Unauthorized"), nil
	}

	// Step 2: Only process pull_request events
	eventType := request.Headers["X-GitHub-Event"]
	if eventType == "" {
		eventType = request.Headers["x-github-event"]
	}

	if eventType != "pull_request" {
		return respond(200, fmt.Sprintf("Event type '%s' ignored", eventType)), nil
	}

	// Step 3: Parse the PR event
	event, err := githubclient.ParsePullRequestEvent([]byte(request.Body))
	if err != nil {
		log.Printf("[Handler] Failed to parse PR event: %v", err)
		return respond(400, "Bad Request"), nil
	}

	// Step 4: Only review when a PR is opened or reopened
	if !githubclient.ShouldReview(event.Action) {
		return respond(200, fmt.Sprintf("Action '%s' ignored", event.Action)), nil
	}

	log.Printf("[Handler] Processing PR #%d in %s (action=%s)",
		event.Number, event.Repository.FullName, event.Action)

	// Step 5: Run the review pipeline asynchronously
	// GitHub expects a response within 10 seconds — we respond immediately
	// and process in the background (Lambda has up to 15 minutes)
	if err := orchestrator.Review(ctx, event); err != nil {
    log.Printf("[Handler] Review failed: %v", err)
    ghClient.PostErrorComment(ctx, event.Installation.ID,
        event.Repository.Owner.Login, event.Repository.Name,
        event.Number, err.Error())
    return respond(500, "Review failed"), nil
}

return respond(200, "Review complete"), nil
}

func respond(statusCode int, message string) events.APIGatewayProxyResponse {
	body, _ := json.Marshal(map[string]string{"message": message})
	return events.APIGatewayProxyResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}
}

func main() {
	lambda.Start(handler)
}

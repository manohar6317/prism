#!/bin/bash
# deploy.sh — Builds and deploys PRism to AWS Lambda + API Gateway
# Prerequisites: AWS CLI configured, SAM CLI installed (pip install aws-sam-cli)
# Run: ./deploy.sh

set -e

STACK_NAME="prism-app"
REGION=${AWS_REGION:-us-east-1}
S3_BUCKET="prism-deploy-$(aws sts get-caller-identity --query Account --output text)"

echo "==> Building Go binary for Lambda (Linux ARM64)"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
  -ldflags="-w -s" \
  -o bootstrap \
  ./cmd/lambda

echo "==> Creating deployment S3 bucket (if not exists)"
aws s3 mb "s3://${S3_BUCKET}" --region "$REGION" 2>/dev/null || true

echo "==> Packaging and deploying to CloudFormation stack: $STACK_NAME"
sam deploy \
  --template-file template.yaml \
  --stack-name "$STACK_NAME" \
  --s3-bucket "$S3_BUCKET" \
  --region "$REGION" \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
    GitHubAppID="${GITHUB_APP_ID}" \
    GitHubPrivateKey="${GITHUB_PRIVATE_KEY}" \
    GitHubWebhookSecret="${GITHUB_WEBHOOK_SECRET}" \
    GeminiAPIKey="${GEMINI_API_KEY}" \
  --no-fail-on-empty-changeset

echo ""
echo "==> Deployment complete!"
echo "Webhook URL:"
aws cloudformation describe-stacks \
  --stack-name "$STACK_NAME" \
  --region "$REGION" \
  --query 'Stacks[0].Outputs[?OutputKey==`WebhookURL`].OutputValue' \
  --output text

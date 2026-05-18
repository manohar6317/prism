# PRism 🔍

An AI-powered automated code review GitHub App built with **Go**, **AWS Lambda**, and **Google Gemini**. Installs on any GitHub repository and automatically reviews every pull request with inline comments, quality scores, and security analysis.

[![CI/CD](https://github.com/manohar6317/prism/actions/workflows/ci.yml/badge.svg)](https://github.com/manohar6317/prism/actions)
![Go Version](https://img.shields.io/badge/go-1.22-blue)
![AWS Lambda](https://img.shields.io/badge/AWS-Lambda%20%7C%20API%20Gateway%20%7C%20DynamoDB-orange)
![Gemini](https://img.shields.io/badge/AI-Google%20Gemini%201.5%20Flash-green)

---

## What It Does

When a developer opens a pull request on any repo with PRism installed:

1. GitHub sends a webhook to PRism's AWS Lambda endpoint
2. PRism fetches the PR diff for each changed file
3. Each diff is sent to Google Gemini with a carefully engineered prompt
4. Gemini returns structured JSON: bugs, security issues, performance problems, suggestions
5. PRism posts inline comments on the exact lines with severity labels
6. A summary comment shows the overall quality score and issue breakdown
7. The review record is persisted to DynamoDB for historical tracking

All of this happens within seconds of the PR being opened — automatically.

---

## Architecture

```
  GitHub
    │
    │  PR opened → webhook POST
    ▼
┌─────────────────────────────────────────────────────┐
│                   AWS Cloud                          │
│                                                      │
│  API Gateway (/webhook)                              │
│       │                                              │
│       │  1. Verify HMAC-SHA256 signature             │
│       ▼                                              │
│  Lambda Function (Go binary on Graviton2)            │
│       │                                              │
│       ├── 2. Parse PR event                          │
│       ├── 3. Fetch diff via GitHub API               │
│       │         (JWT → Installation Token flow)      │
│       │                                              │
│       ├── 4. Per-file: Send diff to Gemini API ──────┼──► Google Gemini
│       │         (structured JSON prompt)             │        (AI Analysis)
│       │                                              │
│       ├── 5. Post inline review comments             │
│       │         → GitHub PR Review API               │
│       │                                              │
│       └── 6. Save review record ──────────────────── ┼──► DynamoDB
│                  (quality score, issues, timestamp)  │    (Review History)
└─────────────────────────────────────────────────────┘
```

---

## Sample Review Output

> PRism posts a summary comment + inline comments on the PR:

```
## 🔍 PRism Code Review

**PR:** Add user authentication endpoint

| Metric       | Value          |
|---|---|
| Quality Score | 🟡 7/10       |
| Issues Found  | 3             |
| Status        | ⚠️ Warnings detected |

### File Summaries
- auth.go: Implements JWT validation correctly but missing token expiry check
- handler.go: Well-structured handler with minor error handling gaps
```

Inline comment example:
```
🚨 [CRITICAL] SQL query is constructed with string concatenation.
This is vulnerable to SQL injection. Use parameterized queries instead:

  db.Query("SELECT * FROM users WHERE id = ?", userID)

Category: security
```

---

## Tech Stack

| Layer | Technology | Why |
|---|---|---|
| Language | Go 1.22 | Fast cold starts, low Lambda memory footprint |
| Compute | AWS Lambda (Graviton2/ARM64) | Serverless, 20% cheaper than x86, scales to zero |
| API | AWS API Gateway | Managed HTTP endpoint for GitHub webhooks |
| Database | AWS DynamoDB | Review history persistence, serverless |
| AI | Google Gemini 1.5 Flash | Fast, capable, generous free tier |
| Auth | GitHub App JWT + Installation Tokens | Secure, scoped, revocable per-repo access |
| CI/CD | GitHub Actions + AWS SAM | Automated deploy on every push to main |

---

## Project Structure

```
prism/
├── cmd/
│   └── lambda/
│       └── main.go              # Lambda handler + initialization
├── internal/
│   ├── github/
│   │   ├── webhook.go           # Signature verification, event parsing
│   │   └── client.go            # GitHub API: fetch diffs, post reviews
│   ├── gemini/
│   │   └── reviewer.go          # Gemini API client + prompt engineering
│   ├── review/
│   │   └── orchestrator.go      # Pipeline: diff → AI → comments → store
│   └── store/
│       └── dynamodb.go          # Review history persistence
├── config/
│   └── config.go                # Environment-based configuration
├── deploy/
│   └── deploy.sh                # One-command deployment script
├── template.yaml                # AWS SAM infrastructure-as-code
├── .github/workflows/ci.yml     # CI/CD: test → build → deploy
└── go.mod
```

---

## Setup Guide

### 1. Create a GitHub App

1. Go to **github.com/settings/apps** → New GitHub App
2. Fill in:
   - **Name:** PRism (or your own name)
   - **Homepage URL:** your GitHub repo URL
   - **Webhook URL:** (placeholder for now — update after deploy)
   - **Webhook Secret:** generate a random string → save it
3. Permissions needed:
   - Pull requests: **Read & Write**
   - Contents: **Read**
4. Subscribe to events: **Pull request**
5. Click Create → Note the **App ID**
6. Scroll down → **Generate a private key** → download the `.pem` file

### 2. Get Gemini API Key

1. Go to **aistudio.google.com**
2. Click **Get API Key** → Create API key
3. Copy the key — it's free, no credit card needed

### 3. Deploy to AWS

```bash
# Clone the repo
git clone https://github.com/manohar6317/prism
cd prism

# Set environment variables
export GITHUB_APP_ID=your_app_id
export GITHUB_PRIVATE_KEY=$(cat your-key.pem | tr '\n' '\\n')
export GITHUB_WEBHOOK_SECRET=your_webhook_secret
export GEMINI_API_KEY=your_gemini_key

# Deploy (builds Go binary + deploys Lambda + API Gateway + DynamoDB)
chmod +x deploy/deploy.sh
./deploy/deploy.sh
```

The script outputs your **Webhook URL**. Copy it.

### 4. Configure GitHub App Webhook

1. Go back to your GitHub App settings
2. Paste the Webhook URL from the deploy output
3. Set Content type to `application/json`

### 5. Install on a Repository

1. Go to your GitHub App page
2. Click **Install** → choose a repo
3. Open a pull request — PRism will review it automatically

---

## Key Engineering Decisions

**Why AWS Lambda instead of a persistent server?**
PRism only runs when a webhook arrives. A persistent server would sit idle 99% of the time. Lambda scales to zero when idle (zero cost) and handles traffic spikes automatically.

**Why GitHub App instead of GitHub Action?**
A GitHub App installs once and works across all PRs — no per-repo configuration. It also uses scoped installation tokens instead of personal access tokens, which is more secure.

**Why HMAC-SHA256 signature verification?**
Without it, anyone who knows your webhook URL could POST fake PR events and trigger reviews. GitHub signs every payload with a shared secret — we verify it before processing anything.

**Why Graviton2 (ARM64) Lambda?**
AWS's ARM-based Graviton2 processors are 20% cheaper and often faster for Go workloads than x86. This is a real engineering tradeoff that shows cost-consciousness.

**Why low temperature (0.2) on Gemini?**
Higher temperature = more creative but less consistent. For code review, we want deterministic, focused analysis — not creative writing. Temperature 0.2 produces reliable structured JSON output.

---

## License

MIT

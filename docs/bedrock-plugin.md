# Bedrock Wire Plugin

AWS Bedrock wire plugin for og. Connects to Bedrock via the ConverseStream API with SigV4 signing.

## Setup

### 1. AWS Credentials

The plugin reads credentials from `~/.aws/credentials` and region from `~/.aws/config`. Supports named profiles.

```bash
# Set profile (optional, defaults to "default")
export AWS_PROFILE=my-profile

# Or use explicit credentials
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
```

### 2. Configuration

Place a `config.toml` next to the plugin binary:

```toml
region       = "us-east-1"      # AWS region (default: us-east-1)
profile      = "my-profile"     # AWS profile name (default: default)
max_tokens   = 4096             # Max output tokens (default: 4096)
endpoint_url = ""               # Custom endpoint URL for VPC endpoints (optional)
```

### 3. Environment Variable Overrides

Env vars take precedence over `config.toml`:

| Variable | Description |
|----------|-------------|
| `OG_BEDROCK_REGION` | AWS region |
| `OG_BEDROCK_PROFILE` | AWS profile name |
| `OG_BEDROCK_MAX_TOKENS` | Max output tokens |
| `OG_BEDROCK_ENDPOINT_URL` | Custom endpoint URL |

Standard AWS env vars are also supported as a lower-priority fallback:

| Variable | Description |
|----------|-------------|
| `AWS_PROFILE` | AWS profile name |
| `AWS_REGION` | AWS region |
| `AWS_DEFAULT_REGION` | AWS region (fallback) |
| `AWS_ACCESS_KEY_ID` | Access key |
| `AWS_SECRET_ACCESS_KEY` | Secret key |
| `AWS_SESSION_TOKEN` | Session token (for temp credentials) |

### Precedence

```
env vars (OG_BEDROCK_*) > config.toml > AWS env vars > ~/.aws/config > defaults
```

### 4. VPC Endpoints

For VPC-only access, set `endpoint_url` in config.toml or `OG_BEDROCK_ENDPOINT_URL`:

```toml
endpoint_url = "https://vpce-xxx.bedrock-runtime.us-east-1.vpce.amazonaws.com"
```

## Models

Available models (use as the model ID in `og -p`):

- `anthropic.claude-sonnet-4-6` — Claude Sonnet 4.6
- `anthropic.claude-opus-4-7` — Claude Opus 4.7
- `anthropic.claude-opus-4-8` — Claude Opus 4.8
- `anthropic.claude-haiku-4-5-20251001-v1:0` — Claude Haiku 4.5
- `meta.llama4-scout-17b-1b-instruct-v1:0` — Llama 4 Scout
- `meta.llama4-maverick-17b-128b-instruct-v1:0` — Llama 4 Maverick
- `meta.llama3-70b-instruct-v1:0` — Llama 3 70B
- `amazon.nova-pro-v1:0` — Nova Pro
- `amazon.nova-lite-v1:0` — Nova Lite
- `mistral.mistral-large-2407-v1:0` — Mistral Large
- `cohere.command-r-v1:0` — Command R

Cross-region inference profiles use prefixed IDs (e.g., `us.anthropic.claude-sonnet-4-6`).

## Usage

```bash
# With default profile
OG_MODEL=anthropic.claude-sonnet-4-6 OG_PROVIDER=bedrock og -p "hello"

# With named profile
AWS_PROFILE=production OG_MODEL=anthropic.claude-sonnet-4-6 OG_PROVIDER=bedrock og -p "hello"

# With config.toml
OG_MODEL=anthropic.claude-sonnet-4-6 OG_PROVIDER=bedrock og -p "hello"
```

## IAM Permissions

The IAM principal needs:

```json
{
  "Effect": "Allow",
  "Action": [
    "bedrock:InvokeModelWithResponseStream"
  ],
  "Resource": "arn:aws:bedrock:*::foundation-model/*"
}
```

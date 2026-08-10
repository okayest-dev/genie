Type: grilling
Status: open
Blocked by:

# 04-change-ledger-schema

## Question

Define the change ledger: the schema of one entry (file, operation, timestamp, old/new content where cheap, tool call id, session id), its per-session JSONL storage and location, and the exact format the `/changes` command prints. Keep it v1-shaped: enough to render a list, cheap enough that the changes view (fog) can build on it later without schema churn.

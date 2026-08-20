#!/bin/bash
# Fake plugin for testing - implements NDJSON-RPC over stdio
# This plugin provides a "greet" tool and a "test-wire" wire

set -euo pipefail

read_request() {
    IFS= read -r line || exit 0
    echo "$line"
}

write_response() {
    echo "$1"
}

handle_request() {
    local request="$1"
    local method=$(echo "$request" | jq -r '.method')
    local id=$(echo "$request" | jq -r '.id')
    local params=$(echo "$request" | jq -c '.params // {}')

    case "$method" in
        "capabilities/list")
            write_response '{"jsonrpc":"2.0","result":{"tools":true,"wires":true,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "tools/list")
            write_response '{"jsonrpc":"2.0","result":{"tools":[{"name":"greet","description":"A greeting tool from plugin","parameters":{"type":"object","properties":{"name":{"type":"string","description":"Name to greet"}},"required":["name"]}}]},"id":'"$id"'}'
            ;;
        "tools/call")
            local tool_name=$(echo "$params" | jq -r '.name')
            local args=$(echo "$params" | jq -c '.arguments // {}')
            if [ "$tool_name" = "greet" ]; then
                local name=$(echo "$args" | jq -r '.name // "World"')
                write_response '{"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"Hello from plugin, '"$name"'!"}]},"id":'"$id"'}'
            else
                write_response '{"jsonrpc":"2.0","error":{"code":-32602,"message":"Unknown tool: '"$tool_name"'"},"id":'"$id"'}'
            fi
            ;;
        "wire/init")
            write_response '{"jsonrpc":"2.0","result":{"ok":true},"id":'"$id"'}'
            ;;
        "wire/list_models")
            write_response '{"jsonrpc":"2.0","result":{"models":[{"id":"fake-gpt-4","name":"Fake GPT-4"},{"id":"fake-claude-3","name":"Fake Claude 3"}]},"id":'"$id"'}'
            ;;
        "wire/stream")
            # Just return a simple response for testing
            write_response '{"jsonrpc":"2.0","result":{"choices":[{"delta":{"content":"Plugin wire response"}}]},"id":'"$id"'}'
            ;;
        "ping")
            write_response '{"jsonrpc":"2.0","result":{"pong":true},"id":'"$id"'}'
            ;;
        "shutdown")
            write_response '{"jsonrpc":"2.0","result":{"ok":true},"id":'"$id"'}'
            exit 0
            ;;
        *)
            write_response '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
}

# Main loop
while true; do
    request=$(read_request)
    if [ -z "$request" ]; then
        break
    fi
    handle_request "$request"
done
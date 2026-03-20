#!/bin/bash
# Smoke test the MCP server via stdio.
# Sends initialize + initialized + tools/list, then optionally calls one tool.

cd ~/skillweave

{
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
  sleep 1
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  sleep 1
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  sleep 1
  if [[ -n "${TOOL_NAME:-}" ]]; then
    echo "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"${TOOL_NAME}\",\"arguments\":{}}}"
    sleep 2
  fi
  sleep 3
} | ./skillweave 2>&1

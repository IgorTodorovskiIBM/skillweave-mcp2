#!/bin/bash
# Test the MCP server via stdio on z/OS
# Sends initialize + initialized + skill_list, captures all output

cd ~/skillweave

{
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
  sleep 1
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  sleep 1
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"skill_list","arguments":{}}}'
  sleep 1
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skill_boot","arguments":{"name":"zos-porting-cli"}}}'
  sleep 5
} | ./skillweave 2>&1

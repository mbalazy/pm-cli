#!/usr/bin/env python3
"""A fake stdio MCP server for the slack source tests: newline-delimited
JSON-RPC, answers initialize/tools/list/tools/call. What each tool call
returns comes from the JSON file FAKE_MCP_SCRIPT names: a map from
"<tool>|<key>" to CSV text, where key = channel_id for conversations_history
and "mention" / "dm" for conversations_search_messages (search_query set =
mention, filter_users_with set = dm). FAKE_MCP_MODE=hang sleeps on every
tool call (the timeout test); FAKE_MCP_MODE=die exits at once. Every call
is appended to FAKE_MCP_LOG."""
import json, os, sys, time

mode = os.environ.get("FAKE_MCP_MODE", "")
if mode == "die":
    sys.stderr.write("fake mcp: dying on purpose\n")
    sys.exit(3)
script = {}
if os.environ.get("FAKE_MCP_SCRIPT"):
    with open(os.environ["FAKE_MCP_SCRIPT"]) as fh:
        script = json.load(fh)
log = os.environ.get("FAKE_MCP_LOG")

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    method, rid = req.get("method"), req.get("id")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": rid, "result": {
            "protocolVersion": req["params"].get("protocolVersion", "2025-06-18"),
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "fake-slack", "version": "0"}}})
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        send({"jsonrpc": "2.0", "id": rid, "result": {"tools": [
            {"name": "conversations_history", "inputSchema": {"type": "object"}},
            {"name": "conversations_search_messages", "inputSchema": {"type": "object"}}]}})
    elif method == "tools/call":
        params = req.get("params", {})
        name, args = params.get("name"), params.get("arguments") or {}
        if log:
            with open(log, "a") as fh:
                fh.write(json.dumps({"tool": name, "args": args}) + "\n")
        if mode == "hang":
            time.sleep(30)
        if name == "conversations_history":
            key = name + "|" + str(args.get("channel_id", ""))
        elif name == "conversations_search_messages":
            key = name + "|" + ("mention" if args.get("search_query") else "dm")
        else:
            key = name
        if key in script:
            send({"jsonrpc": "2.0", "id": rid, "result": {"content": [{"type": "text", "text": script[key]}]}})
        else:
            send({"jsonrpc": "2.0", "id": rid, "result": {"isError": True, "content": [{"type": "text", "text": "no such channel: " + key}]}})
    elif rid is not None:
        send({"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "unknown method " + str(method)}})

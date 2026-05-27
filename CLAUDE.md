# cli-agent

A terminal-based AI agent that runs against local Ollama models with tool use (file I/O, shell, web). Built in Go with the Charm BubbleTea TUI framework.

## Quick Start

```bash
# Ensure Ollama is running with models pulled
ollama serve
ollama pull gemma4:e2b

# Build and run
go build ./cmd/cli-agent && ./cli-agent

# Headless mode
./cli-agent --headless --prompt "list files in the current directory"
```

## Architecture

```
cmd/cli-agent/main.go          Entry point: flags, config loading, wiring
internal/
  ollama/client.go             HTTP client for Ollama /api/chat (NDJSON streaming), /api/show (model info)
  ollama/types.go              Request/response types, tool schemas, ShowResponse
  agent/agent.go               ReAct loop (Send), summarization (Compact), tool execution, token tracking
  agent/context.go             Conversation history with sliding window (40 msgs)
  agent/system_prompt.go       System prompt builder
  tools/registry.go            Tool interface + registry (lookup by name)
  tools/filesystem.go          read_file, write_file, list_dir, find_files
  tools/shell.go               run_shell (requires approval)
  tools/web.go                 fetch_url, web_search (DDG HTML scraping)
  config/config.go             JSON config from $XDG_CONFIG_HOME/cli-agent/config.json
  history/history.go           Session save/load/list/update/rename/delete (persistence)
  config/config.go             JSON config from $XDG_CONFIG_HOME/cli-agent/config.json
  tui/app.go                   BubbleTea model: state machine, streaming, slash commands, autocomplete
  tui/messages.go              tea.Msg types for the event loop
  tui/styles.go                Lipgloss style definitions
  tui/approval.go              Approval modal component
  tui/modelpicker.go           Model selection list component
  tui/historypicker.go         History browser list component
```

## Data Flow

### Chat

```
User types in textarea → enter key → sendMessage Cmd starts agent
  ↓
Agent.Send() runs in goroutine, pushes tokens/events to streamCh
  ↓
waitForStreamMsg Cmd reads from streamCh → Update() processes:
  StreamTokenMsg    → append to viewport (raw text + ▌ cursor, no glamour)
  ToolEventMsg      → show tool result in viewport
  ApprovalRequestMsg → show approval modal, block agent until response
  StreamDoneMsg     → finalize streaming, render with glamour markdown, auto-save, check context usage warning
  StreamErrMsg      → show error
  ModelContextMsg   → store model context length from /api/show
```

### Compact

```
User types /compact → cmdCompact Cmd starts agent.Compact()
  ↓
Agent formats conversation as plain text, sends summarization request (temp 0.3)
  ↓
Summary streams to viewport via streamCh (same pattern as chat)
  ↓
On completion, conversation history replaced with 2-message summary
```

All streaming messages flow through a single `streamCh` channel. The `sendMessage` Cmd returns nil — everything is routed through the channel to avoid goroutine leaks.

### Queued Messages

Users can type and send messages while the agent is streaming. Pressing Enter during streaming cancels the current response and queues the new message. The queued message appears in chat immediately and is sent to the LLM as the next request. Multiple messages can be queued — they are processed one by one as each response completes. Pressing ESC cancels streaming and discards the queue.

### Non-Tool Models

Models that don't support Ollama's tool calling API (e.g. deepseek-r1) are handled gracefully: on a 400 "does not support tools" error, the agent retries the request without tool definitions, falling back to plain chat mode.

## Tool System

Tools implement the `Tool` interface:

```go
type Tool interface {
    Definition() ollama.Tool       // JSON schema for the LLM
    Execute(ctx, args string) (string, error)
    NeedsApproval() bool
}
```

| Tool | Approval | Description |
|------|----------|-------------|
| read_file | no | Read file contents (50KB limit) |
| write_file | yes | Write/create files (creates dirs) |
| list_dir | no | List directory contents |
| find_files | no | Glob-based file search |
| run_shell | yes | Execute shell commands (30s timeout) |
| fetch_url | no | Fetch URL, strip HTML to text (1MB limit) |
| web_search | no | DDG HTML scraping for search results |

Tool results are sent back as `role: "tool"` messages with `tool_name` matching the function name (required by Ollama).

Approval-gated tools (`write_file`, `run_shell`) show a modal by default. Tools can be auto-approved via the `auto_approve` config map.

## Agent Methods

| Method | Description |
|--------|-------------|
| `Send(ctx, msg, tokenCb, toolEvent)` | Main ReAct loop: sends message, executes tool calls, re-sends until no more tool calls. Captures `PromptEvalCount` from final response. |
| `Compact(ctx, tokenCb)` | Summarizes conversation history, replaces it with a 2-message summary pair |
| `Messages()` | Returns current conversation history |
| `SetMessages(msgs)` | Restores conversation history (used by /load) |
| `LastPromptTokens` | Tokens used in the last prompt (from Ollama `prompt_eval_count`) |
| `ModelContextLen` | Model's native context window (fetched from `/api/show`, falls back to config) |

## Configuration

Config loaded from `$XDG_CONFIG_HOME/cli-agent/config.json` (defaults to `~/.config/cli-agent/config.json`). See `config.example.json` for a template. Created with defaults on first run:

```json
{
  "ollama_url": "http://localhost:11434",
  "model": "",
  "temperature": 0.7,
  "context_length": 8192,
  "max_messages": 40,
  "auto_approve": {
    "read_file": true,
    "list_dir": true,
    "find_files": true,
    "fetch_url": true,
    "web_search": true
  }
}
```

CLI flags override config values.

## Session Persistence

Sessions are stored as JSON in `~/.cache/cli-agent/sessions/<timestamp>.json`.

- **Auto-save** triggers after each completed response and on quit (Ctrl+D, /exit, /quit)
- First save creates a new session; subsequent saves update the existing one
- Sessions have a friendly name (auto-generated from first user message, or set via `/save <name>`)
- `/save [name]` saves with optional friendly name; updates name on existing sessions
- `/load <name-or-id>` restores session by name prefix, ID prefix, or exact ID
- `/history` lists sessions with friendly names and timestamps (press `d` to delete, `enter` to load)
- `/delete <name-or-id>` deletes a saved session by name or ID

## Key Design Decisions

- **Ollama HTTP API** over CLI wrapper or llama.cpp — native streaming, tool calling, model management without subprocess overhead
- **Channel-based streaming** — `streamCh` carries all messages from the agent goroutine to BubbleTea's Update loop. No global `*tea.Program` reference. Cmds return nil to avoid goroutine leaks.
- **Raw text during streaming, glamour on completion** — avoids per-token markdown rendering overhead. Streaming entries show raw text with `▌` cursor; finalized entries render with glamour.
- **`FunctionCall.Arguments` as `json.RawMessage`** — Ollama can send arguments as either a JSON string or object depending on model/version. `ArgsString()` normalizes both.
- **`Message.ToolName`** — required by Ollama to match tool results to tool calls in multi-tool responses
- **Compact over blind truncation** — `/compact` asks the model to summarize before the sliding window drops old messages silently
- **Alt+Enter for newline** — Shift+Enter is indistinguishable from Enter in most terminals (both send `\r`). Alt+Enter sends `\x1b\r` which BubbleTea can detect.
- **`/exit` and `/quit` to quit** — Ctrl+Q clashed with macOS shortcuts. Ctrl+D quits when input is empty. Ctrl+C is reserved for canceling streaming.
- **XDG-compliant config path** — uses `$XDG_CONFIG_HOME` (defaults to `~/.config`) instead of Go's `os.UserConfigDir()` which resolves to `~/Library/Application Support` on macOS.
- **Graceful non-tool model fallback** — models that don't support tools get a retry without tool definitions, enabling plain chat with any Ollama model.
- **Context-aware compact warning** — token usage is tracked via Ollama's `prompt_eval_count` (no tokenizer dependency). Model context length is fetched from `/api/show` (`model_info.<family>.context_length`). Header shows `ctx:Xk/Yk`; warning appears at 80% usage.
- **Inline history deletion** — press `d` in `/history` picker to delete sessions without leaving the list.

## Slash Commands

| Command | Description |
|---------|-------------|
| /model | Switch active model |
| /clear | Clear conversation history |
| /compact | Summarize conversation to free context window |
| /save [name] | Save session (optional friendly name) |
| /load <name-or-id> | Load a saved session |
| /history | Browse saved sessions (interactive list) |
| /delete <name-or-id> | Delete a saved session |
| /exit, /quit | Quit (auto-saves) |
| /help | Show help |

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| Enter | Send message / accept autocomplete / queue message while streaming |
| Alt+Enter | New line in input |
| Tab | Accept autocomplete suggestion |
| Ctrl+D | Quit when input empty (auto-saves) |
| Ctrl+C | Cancel streaming |
| ESC | Cancel streaming and discard queued messages |
| Ctrl+L | Clear conversation |
| Ctrl+Up/Ctrl+Down | Resize input area vertically |
| PageUp/PageDown | Scroll chat history |
| Up/Down | Scroll chat history (when input is empty) |
| d | Delete selected session (in /history picker) |

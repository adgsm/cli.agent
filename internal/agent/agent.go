package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mdzunic/cli-agent/internal/ollama"
	"github.com/mdzunic/cli-agent/internal/tools"
)

// TokenCallback is called with each streamed token. Empty string = model paused (tool call).
type TokenCallback func(token string)

// ToolApprovalFunc is called before executing a tool that requires approval.
// Return true to allow, false to deny.
type ToolApprovalFunc func(toolName, args string) bool

// Agent runs the ReAct loop against a local Ollama model.
type Agent struct {
	client           *ollama.Client
	registry         *tools.Registry
	conv             *ConvContext
	model            string
	approval         ToolApprovalFunc
	LastPromptTokens int // tokens used in the last completed prompt
	ModelContextLen  int // model's native context window (0 = unknown)
}

func New(client *ollama.Client, registry *tools.Registry, model string, approval ToolApprovalFunc) *Agent {
	return &Agent{
		client:   client,
		registry: registry,
		conv:     NewConvContext(),
		model:    model,
		approval: approval,
	}
}

func (a *Agent) SetModel(model string) {
	a.model = model
}

func (a *Agent) SetApproval(fn ToolApprovalFunc) {
	a.approval = fn
}

func (a *Agent) Model() string {
	return a.model
}

func (a *Agent) ClearHistory() {
	a.conv.Clear()
}

func (a *Agent) Messages() []ollama.Message {
	return a.conv.Messages()
}

func (a *Agent) SetMessages(msgs []ollama.Message) {
	a.conv.Set(msgs)
}

// Compact asks the model to summarize the conversation so far, then replaces
// the history with the summary. tokenCb streams the summary as it is generated.
func (a *Agent) Compact(ctx context.Context, tokenCb TokenCallback) error {
	if len(a.conv.Messages()) == 0 {
		return fmt.Errorf("nothing to compact")
	}

	compactPrompt := `Summarize the conversation below into a concise summary that preserves:
1. What the user asked for and what was accomplished
2. Key files, paths, and code that were discussed or modified
3. Any important decisions or constraints mentioned
4. The current state of work (what's done, what's pending)

Write the summary in a way that an AI assistant could pick up the conversation and continue seamlessly. Do not add any preamble — just write the summary directly.`

	msgs := a.conv.Messages()

	req := ollama.ChatRequest{
		Model: a.model,
		Messages: []ollama.Message{
			{Role: "system", Content: compactPrompt},
			{Role: "user", Content: formatConversationForSummary(msgs)},
		},
		Stream: true,
		Options: &ollama.Options{
			Temperature: 0.3,
		},
	}

	var summary strings.Builder
	var finalResponse ollama.ChatResponse

	err := a.client.Chat(ctx, req, func(token string, chunk ollama.ChatResponse, done bool) {
		if !done && token != "" {
			summary.WriteString(token)
			tokenCb(token)
		}
		if done {
			finalResponse = chunk
		}
	})
	if err != nil {
		return err
	}

	// Replace conversation with just the summary as context.
	a.conv.Set([]ollama.Message{
		{Role: "user", Content: "[Previous conversation summary]"},
		{Role: "assistant", Content: finalResponse.Message.Content},
	})

	return nil
}

// formatConversationForSummary renders the message history as plain text for the summarizer.
func formatConversationForSummary(msgs []ollama.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			fmt.Fprintf(&sb, "User: %s\n\n", m.Content)
		case "assistant":
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					fmt.Fprintf(&sb, "Assistant called tool %s with args: %s\n", tc.Function.Name, tc.Function.ArgsString())
				}
			}
			if m.Content != "" {
				fmt.Fprintf(&sb, "Assistant: %s\n\n", m.Content)
			}
		case "tool":
			fmt.Fprintf(&sb, "Tool result: %s\n\n", m.Content)
		}
	}
	return sb.String()
}

// Send sends a user message and runs the ReAct loop until the model produces
// a final text response (no more tool calls). tokenCb is called with each
// streamed token; toolEvent is called when a tool executes (for display).
func (a *Agent) Send(ctx context.Context, userMsg string, tokenCb TokenCallback, toolEvent func(name, args, result string)) error {
	a.conv.Add(ollama.Message{Role: "user", Content: userMsg})

	toolDefs := a.registry.Definitions()
	systemMsg := ollama.Message{
		Role:    "system",
		Content: buildSystemPrompt(toolDefs),
	}

	for {
		msgs := append([]ollama.Message{systemMsg}, a.conv.Messages()...)

		var finalResponse ollama.ChatResponse

		req := ollama.ChatRequest{
			Model:    a.model,
			Messages: msgs,
			Tools:    toolDefs,
			Stream:   true,
		}

		err := a.client.Chat(ctx, req, func(token string, chunk ollama.ChatResponse, done bool) {
			if !done && token != "" {
				tokenCb(token)
			}
			if done {
				finalResponse = chunk
			}
		})

		// If the model doesn't support tools, retry without them.
		if err != nil && strings.Contains(err.Error(), "does not support tools") {
			req.Tools = nil
			systemMsg.Content = buildSystemPrompt(nil)
			msgs = append([]ollama.Message{systemMsg}, a.conv.Messages()...)
			req.Messages = msgs

			err = a.client.Chat(ctx, req, func(token string, chunk ollama.ChatResponse, done bool) {
				if !done && token != "" {
					tokenCb(token)
				}
				if done {
					finalResponse = chunk
				}
			})
		}

		if err != nil {
			return err
		}

		a.LastPromptTokens = finalResponse.PromptEvalCount

		assistantMsg := finalResponse.Message
		a.conv.Add(assistantMsg)

		// No tool calls → we are done.
		if len(assistantMsg.ToolCalls) == 0 {
			return nil
		}

		// Execute each tool call and feed results back into the conversation.
		for _, tc := range assistantMsg.ToolCalls {
			result, execErr := a.executeTool(ctx, tc)
			if toolEvent != nil {
				toolEvent(tc.Function.Name, tc.Function.ArgsString(), result)
			}
			toolMsg := ollama.Message{
				Role:     "tool",
				Content:  result,
				ToolName: tc.Function.Name,
			}
			if execErr != nil {
				toolMsg.Content = fmt.Sprintf("[error: %v]", execErr)
			}
			a.conv.Add(toolMsg)
		}
		// Loop: send updated conversation back to the model.
	}
}

func (a *Agent) executeTool(ctx context.Context, tc ollama.ToolCall) (string, error) {
	tool, ok := a.registry.Get(tc.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tc.Function.Name)
	}

	if tool.NeedsApproval() && a.approval != nil {
		prettyArgs := prettyJSON(tc.Function.ArgsString())
		if !a.approval(tc.Function.Name, prettyArgs) {
			return "[user denied tool execution]", nil
		}
	}

	return tool.Execute(ctx, tc.Function.ArgsString())
}

func prettyJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return string(b)
}

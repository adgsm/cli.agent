package agent

import (
	"fmt"
	"strings"

	"github.com/mdzunic/cli-agent/internal/ollama"
)

func buildSystemPrompt(tools []ollama.Tool) string {
	var sb strings.Builder
	sb.WriteString(`You are a helpful AI assistant running locally with access to tools.
You can read and write files, run shell commands, and search the web.

Guidelines:
- Be concise and direct in your responses.
- When you need information from the filesystem or web, use the appropriate tool rather than guessing.
- For shell commands, explain what the command does before requesting approval.
- Always prefer reading existing files before suggesting changes.
- Format code blocks with markdown fences and language identifiers.
`)

	if len(tools) > 0 {
		sb.WriteString("\nAvailable tools:\n")
		for _, t := range tools {
			fmt.Fprintf(&sb, "- %s: %s\n", t.Function.Name, t.Function.Description)
		}
	}
	return sb.String()
}

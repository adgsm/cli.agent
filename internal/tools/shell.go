package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/mdzunic/cli-agent/internal/ollama"
)

const shellTimeout = 30 * time.Second

type RunShellTool struct{}

func (t *RunShellTool) NeedsApproval() bool { return true }

func (t *RunShellTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "run_shell",
			Description: "Run a shell command and return its stdout and stderr output.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"command": {Type: "string", Description: "The shell command to execute (run via /bin/sh -c)."},
				},
				Required: []string{"command"},
			},
		},
	}
}

func (t *RunShellTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	command := stringArg(m, "command")
	if command == "" {
		return "", fmt.Errorf("command is required")
	}

	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	var result string
	if stdout.Len() > 0 {
		result += stdout.String()
	}
	if stderr.Len() > 0 {
		result += "\n[stderr]\n" + stderr.String()
	}
	if runErr != nil {
		result += fmt.Sprintf("\n[exit error: %v]", runErr)
	}
	if result == "" {
		result = "(no output)"
	}
	return result, nil
}

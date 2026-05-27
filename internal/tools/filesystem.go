package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdzunic/cli-agent/internal/ollama"
)

const maxFileBytes = 50_000

// ReadFileTool reads a file from disk.
type ReadFileTool struct{}

func (t *ReadFileTool) NeedsApproval() bool { return false }

func (t *ReadFileTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "read_file",
			Description: "Read the contents of a file on the local filesystem.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"path": {Type: "string", Description: "Absolute or relative path to the file."},
				},
				Required: []string{"path"},
			},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	path := stringArg(m, "path")
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > maxFileBytes {
		return string(data[:maxFileBytes]) + fmt.Sprintf("\n\n[truncated — file is %d bytes, showing first %d]", len(data), maxFileBytes), nil
	}
	return string(data), nil
}

// WriteFileTool writes content to a file.
type WriteFileTool struct{}

func (t *WriteFileTool) NeedsApproval() bool { return true }

func (t *WriteFileTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "write_file",
			Description: "Write content to a file on the local filesystem. Creates parent directories if needed.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"path":    {Type: "string", Description: "Path to the file to write."},
					"content": {Type: "string", Description: "Content to write into the file."},
				},
				Required: []string{"path", "content"},
			},
		},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	path := stringArg(m, "path")
	content := stringArg(m, "content")
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("written %d bytes to %s", len(content), path), nil
}

// ListDirTool lists directory contents.
type ListDirTool struct{}

func (t *ListDirTool) NeedsApproval() bool { return false }

func (t *ListDirTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "list_dir",
			Description: "List files and directories at a given path.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"path":      {Type: "string", Description: "Directory path to list."},
					"recursive": {Type: "boolean", Description: "Whether to list recursively."},
				},
				Required: []string{"path"},
			},
		},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	path := stringArg(m, "path")
	recursive := boolArg(m, "recursive")
	if path == "" {
		path = "."
	}

	var sb strings.Builder
	if recursive {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(path, p)
			if d.IsDir() {
				sb.WriteString(rel + "/\n")
			} else {
				sb.WriteString(rel + "\n")
			}
			return nil
		})
	} else {
		entries, err2 := os.ReadDir(path)
		if err2 != nil {
			return "", err2
		}
		for _, e := range entries {
			if e.IsDir() {
				sb.WriteString(e.Name() + "/\n")
			} else {
				sb.WriteString(e.Name() + "\n")
			}
		}
		err = nil
	}
	if err != nil {
		return "", err
	}
	result := sb.String()
	if result == "" {
		return "(empty directory)", nil
	}
	return result, nil
}

// FindFilesTool finds files matching a glob pattern.
type FindFilesTool struct{}

func (t *FindFilesTool) NeedsApproval() bool { return false }

func (t *FindFilesTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "find_files",
			Description: "Find files matching a glob pattern within a directory.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"pattern": {Type: "string", Description: "Glob pattern, e.g. '*.go' or '**/*.json'."},
					"dir":     {Type: "string", Description: "Root directory to search from (default: current directory)."},
				},
				Required: []string{"pattern"},
			},
		},
	}
}

func (t *FindFilesTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	pattern := stringArg(m, "pattern")
	dir := stringArg(m, "dir")
	if dir == "" {
		dir = "."
	}
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	var matches []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "no files found matching " + pattern, nil
	}
	return strings.Join(matches, "\n"), nil
}

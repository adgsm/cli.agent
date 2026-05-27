package tools

import (
	"context"
	"encoding/json"

	"github.com/mdzunic/cli-agent/internal/ollama"
)

// Tool is the interface every tool must implement.
type Tool interface {
	// Definition returns the Ollama tool schema for this tool.
	Definition() ollama.Tool
	// Execute runs the tool with the given JSON-encoded arguments.
	Execute(ctx context.Context, args string) (string, error)
	// NeedsApproval returns true if the tool should prompt the user before running.
	NeedsApproval() bool
}

// Registry holds all registered tools and provides lookup by name.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Definition().Function.Name] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Definitions() []ollama.Tool {
	defs := make([]ollama.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

// DefaultRegistry returns a registry with all built-in tools registered.
func DefaultRegistry(ollamaURL string) *Registry {
	r := NewRegistry()
	r.Register(&ReadFileTool{})
	r.Register(&WriteFileTool{})
	r.Register(&ListDirTool{})
	r.Register(&FindFilesTool{})
	r.Register(&RunShellTool{})
	r.Register(&FetchURLTool{})
	r.Register(&WebSearchTool{})
	r.Register(&OCRTool{OllamaURL: ollamaURL, OCRModel: "glm-ocr:bf16"})
	return r
}

// UnmarshalArgs is a helper to decode tool arguments from JSON into a map.
func UnmarshalArgs(raw string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

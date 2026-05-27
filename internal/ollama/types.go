package ollama

import (
	"encoding/json"
	"strings"
)

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds a tool invocation. Arguments is kept as raw JSON so it
// can be either a string or an object depending on the model/version.
type FunctionCall struct {
	Index     int             `json:"index,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ArgsString returns the arguments as a compact JSON string, suitable for
// passing to tool Execute methods (which expect a JSON-encoded string).
func (f FunctionCall) ArgsString() string {
	if len(f.Arguments) == 0 {
		return "{}"
	}
	// If already a JSON string (quoted), unwrap it.
	if f.Arguments[0] == '"' {
		var s string
		if json.Unmarshal(f.Arguments, &s) == nil {
			return s
		}
	}
	return string(f.Arguments)
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ToolParameters `json:"parameters"`
}

type ToolParameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
	Options  *Options  `json:"options,omitempty"`
}

type Options struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumCtx      int     `json:"num_ctx,omitempty"`
}

type ChatResponse struct {
	Model   string  `json:"model"`
	Message Message `json:"message"`
	Done    bool    `json:"done"`

	// populated only when done=true
	DoneReason         string `json:"done_reason,omitempty"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
}

type ModelInfo struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
	Details    struct {
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
}

type TagsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ShowResponse is the response from POST /api/show.
type ShowResponse struct {
	Details   ShowDetails        `json:"details"`
	ModelInfo map[string]any     `json:"model_info"`
}

type ShowDetails struct {
	ParentModel     string   `json:"parent_model"`
	Format          string   `json:"format"`
	Family          string   `json:"family"`
	Families        []string `json:"families"`
	ParameterSize   string   `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

// ContextLength extracts the model's native context window from model_info.
// The key is family-specific (e.g., "gemma4.context_length", "llama.context_length").
func (s ShowResponse) ContextLength() int {
	for k, v := range s.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			switch n := v.(type) {
			case float64:
				return int(n)
			case json.Number:
				i, _ := n.Int64()
				return int(i)
			}
		}
	}
	return 0
}

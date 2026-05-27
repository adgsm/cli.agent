package tui

// StreamTokenMsg carries a single streamed token from the model.
type StreamTokenMsg struct {
	Token string
}

// StreamDoneMsg signals that the model has finished its turn.
type StreamDoneMsg struct {
	PromptTokens int // tokens used in the prompt (from Ollama prompt_eval_count)
}

// StreamErrMsg carries an error from the streaming goroutine.
type StreamErrMsg struct {
	Err error
}

// ToolEventMsg carries a tool execution event for display.
type ToolEventMsg struct {
	Name   string
	Args   string
	Result string
}

// ApprovalRequestMsg is sent when a tool that needs approval is about to run.
type ApprovalRequestMsg struct {
	ToolName string
	Args     string
	Response chan bool
}

// ModelsLoadedMsg carries the list of available models after startup.
type ModelsLoadedMsg struct {
	Models []string
}

// ModelsErrMsg carries an error loading models.
type ModelsErrMsg struct {
	Err error
}

// ModelContextMsg carries the model's context window length.
type ModelContextMsg struct {
	ContextLength int
}

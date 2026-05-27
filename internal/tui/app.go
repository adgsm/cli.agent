package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mdzunic/cli-agent/internal/agent"
	"github.com/mdzunic/cli-agent/internal/config"
	"github.com/mdzunic/cli-agent/internal/history"
	"github.com/mdzunic/cli-agent/internal/ollama"
)

type appState int

const (
	stateLoading      appState = iota // waiting for model list
	statePickModel                    // model picker screen
	stateChat                         // main chat screen
	stateStreaming                    // model is generating
	stateApproval                     // waiting for tool approval
	stateHistory                      // history picker screen
)

// App is the root Bubbletea model.
type App struct {
	state        appState
	ollamaClient *ollama.Client
	ag           *agent.Agent
	cfg          *config.Config
	conv         []chatEntry // display entries

	// sub-models
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	picker   modelPicker
	hpicker  historyPicker
	approval approvalModal

	width, height int
	inputHeight   int // textarea height in lines (resizable with ctrl+up/ctrl+down)
	renderer      *glamour.TermRenderer
	cancelStream  context.CancelFunc
	pickerReady   bool

	// streamCh carries streaming tokens/tool events from the agent goroutine
	// to the BubbleTea update loop, avoiding the need for a global *tea.Program.
	streamCh chan tea.Msg

	// session tracking for persistence
	sessionID string // empty = new session, non-empty = loaded/saved session

	// context tracking
	lastPromptTokens int // tokens used in the last prompt
	modelContextLen   int // model's native context window (0 = unknown)

	// messages queued while streaming, sent automatically one by one as responses finish
	pendingMsgs []string

	// slash command autocomplete
	autoVisible  bool
	autoItems    []slashCmd
	autoSelected int
}

type chatEntry struct {
	role    string // "user" | "assistant" | "assistant-stream" | "tool" | "error"
	content string
}

type slashCmd struct {
	name string
	desc string
}

var slashCommands = []slashCmd{
	{"/model", "Switch the active model"},
	{"/clear", "Clear conversation history"},
	{"/compact", "Summarize conversation to free context"},
	{"/save", "Save session (optional name)"},
	{"/load", "Load a saved session"},
	{"/history", "Browse saved sessions"},
	{"/delete", "Delete a saved session"},
	{"/exit", "Quit (auto-saves)"},
	{"/quit", "Quit (auto-saves)"},
	{"/help", "Show help"},
}

// New creates the root App model.
func New(client *ollama.Client, ag *agent.Agent, cfg *config.Config) *App {
	ta := textarea.New()
	ta.Placeholder = "Message... (Enter send, Alt+Enter newline)"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	vp := viewport.New(80, 20)
	vp.SetContent("")

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)

	return &App{
		state:        stateLoading,
		ollamaClient: client,
		ag:           ag,
		cfg:          cfg,
		viewport:     vp,
		textarea:     ta,
		spinner:      sp,
		renderer:     renderer,
		streamCh:     make(chan tea.Msg, 64),
		inputHeight:  3,
	}
}

// Init starts the app by fetching model list.
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.spinner.Tick,
		a.loadModels(),
	)
}

func (a *App) loadModels() tea.Cmd {
	return func() tea.Msg {
		models, err := a.ollamaClient.ListModels(context.Background())
		if err != nil {
			return ModelsErrMsg{Err: err}
		}
		names := make([]string, len(models))
		for i, m := range models {
			names[i] = m.Name
		}
		return ModelsLoadedMsg{Models: names}
	}
}

// waitForStreamMsg reads the next message from the stream channel.
// Called in a loop by Update to keep consuming tokens.
func (a *App) waitForStreamMsg() tea.Cmd {
	return func() tea.Msg {
		return <-a.streamCh
	}
}

// Update handles all messages.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.resizeComponents()

	case tea.KeyMsg:
		return a.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd

	case ModelsLoadedMsg:
		if len(msg.Models) == 0 {
			a.addEntry("error", "No models found. Run: ollama pull gemma4:e2b")
			a.state = stateChat
			return a, nil
		}
		a.picker = newModelPicker(msg.Models, a.width, a.height-4)
		a.pickerReady = true
		a.state = statePickModel
		return a, nil

	case ModelsErrMsg:
		a.addEntry("error", fmt.Sprintf("Failed to connect to Ollama: %v\n\nMake sure Ollama is running: ollama serve", msg.Err))
		a.state = stateChat
		return a, nil

	case StreamTokenMsg:
		a.appendToken(msg.Token)
		a.viewport.GotoBottom()
		return a, a.waitForStreamMsg()

	case StreamDoneMsg:
		a.finalizeStreaming()
		a.lastPromptTokens = msg.PromptTokens
		a.refreshViewport()
		a.viewport.GotoBottom()
		a.autoSave()
		// Warn at 80% context usage.
		if a.modelContextLen > 0 && a.lastPromptTokens > 0 {
			pct := float64(a.lastPromptTokens) / float64(a.modelContextLen)
			if pct > 0.8 {
				a.addEntry("error", fmt.Sprintf("Context usage: %d/%d tokens (%.0f%%). Consider /compact to free space.",
					a.lastPromptTokens, a.modelContextLen, pct*100))
				a.refreshViewport()
			}
		}
		if len(a.pendingMsgs) > 0 {
			next := a.pendingMsgs[0]
			a.pendingMsgs = a.pendingMsgs[1:]
			return a, a.sendQueuedMessage(next)
		}
		a.state = stateChat
		a.textarea.Focus()
		a.textarea.Placeholder = "Message... (Enter send, Alt+Enter newline)"
		return a, nil

	case StreamErrMsg:
		a.finalizeStreaming()
		a.state = stateChat
		a.addEntry("error", fmt.Sprintf("Error: %v", msg.Err))
		a.refreshViewport()
		a.textarea.Placeholder = "Message... (Enter send, Alt+Enter newline)"
		return a, nil

	case ToolEventMsg:
		a.addEntry("tool", fmt.Sprintf("[%s]\n%s\n\nResult: %s", msg.Name, msg.Args, msg.Result))
		a.refreshViewport()
		a.viewport.GotoBottom()
		return a, a.waitForStreamMsg()

	case ModelContextMsg:
		if msg.ContextLength > 0 {
			a.modelContextLen = msg.ContextLength
			a.ag.ModelContextLen = msg.ContextLength
		} else {
			a.modelContextLen = a.cfg.ContextLen
			a.ag.ModelContextLen = a.cfg.ContextLen
		}
		return a, nil

	case ApprovalRequestMsg:
		a.state = stateApproval
		a.approval = approvalModal{
			toolName: msg.ToolName,
			args:     msg.Args,
			response: msg.Response,
			width:    a.width,
		}
		return a, nil

	case approvalResultMsg:
		msg.response <- msg.allowed
		a.state = stateStreaming
		return a, a.waitForStreamMsg()
	}

	// Delegate to sub-models based on state.
	switch a.state {
	case statePickModel:
		var cmd tea.Cmd
		a.picker, cmd = a.picker.Update(msg)
		cmds = append(cmds, cmd)

	case stateChat:
		var cmd tea.Cmd
		a.textarea, cmd = a.textarea.Update(msg)
		cmds = append(cmds, cmd)
		a.viewport, cmd = a.viewport.Update(msg)
		cmds = append(cmds, cmd)

	case stateStreaming:
		a.viewport, _ = a.viewport.Update(msg)

	case stateApproval:
		var cmd tea.Cmd
		a.approval, cmd = a.approval.Update(msg)
		cmds = append(cmds, cmd)

	case stateHistory:
		var cmd tea.Cmd
		a.hpicker, cmd = a.hpicker.Update(msg)
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.state {
	case statePickModel:
		if msg.String() == "enter" {
			chosen := a.picker.selectedModel()
			if chosen != "" {
				a.ag.SetModel(chosen)
				a.state = stateChat
				a.addEntry("assistant", fmt.Sprintf("Using model **%s**. How can I help?", chosen))
				a.refreshViewport()
				a.viewport.GotoBottom()
				a.textarea.Focus()
				return a, a.fetchModelContext(chosen)
			}
			return a, nil
		}
		var cmd tea.Cmd
		a.picker, cmd = a.picker.Update(msg)
		return a, cmd

	case stateApproval:
		var cmd tea.Cmd
		a.approval, cmd = a.approval.Update(msg)
		return a, cmd

	case stateHistory:
		switch msg.String() {
		case "enter":
			if s := a.hpicker.selectedSession(); s != nil {
				a.loadSession(s)
			}
			return a, nil
		case "d":
			if s := a.hpicker.selectedSession(); s != nil {
				label := s.ID
				if s.Name != "" {
					label = s.Name
				}
				_ = history.Delete(s.ID)
				if a.sessionID == s.ID {
					a.sessionID = ""
				}
				a.hpicker.removeItem(s.ID)
				if len(a.hpicker.list.Items()) == 0 {
					a.state = stateChat
					a.textarea.Focus()
					a.addEntry("assistant", "Deleted session **"+label+"**. No more saved sessions.")
					a.refreshViewport()
				}
			}
			return a, nil
		case "esc":
			a.state = stateChat
			a.textarea.Focus()
			return a, nil
		}
		var cmd tea.Cmd
		a.hpicker, cmd = a.hpicker.Update(msg)
		return a, cmd

	case stateStreaming:
		switch msg.String() {
		case "ctrl+c", "ctrl+z", "esc":
			if a.cancelStream != nil {
				a.cancelStream()
			}
			a.finalizeStreaming()
			a.pendingMsgs = nil
			a.state = stateChat
			a.textarea.Placeholder = "Message... (Enter send, Alt+Enter newline)"
			a.textarea.Focus()
			a.refreshViewport()
			return a, nil
		case "enter":
			input := strings.TrimSpace(a.textarea.Value())
			if input != "" {
				a.pendingMsgs = append(a.pendingMsgs, input)
				a.textarea.Reset()
				a.addEntry("user", input)
				a.refreshViewport()
				a.viewport.GotoBottom()
				if a.cancelStream != nil {
					a.cancelStream()
				}
			}
			return a, nil
		case "ctrl+up":
			a.resizeInput(1)
			return a, nil
		case "ctrl+down":
			a.resizeInput(-1)
			return a, nil
		case "pgup":
			a.viewport.HalfPageUp()
			return a, nil
		case "pgdown":
			a.viewport.HalfPageDown()
			return a, nil
		case "up":
			if a.textarea.Value() == "" {
				a.viewport.ScrollUp(1)
				return a, nil
			}
		case "down":
			if a.textarea.Value() == "" {
				a.viewport.ScrollDown(1)
				return a, nil
			}
		}
		var cmd tea.Cmd
		a.textarea, cmd = a.textarea.Update(msg)
		a.viewport, _ = a.viewport.Update(msg)
		return a, cmd

	case stateChat:
		// Autocomplete navigation takes priority when visible.
		if a.autoVisible {
			switch msg.String() {
			case "up":
				if a.autoSelected > 0 {
					a.autoSelected--
				}
				return a, nil
			case "down":
				if a.autoSelected < len(a.autoItems)-1 {
					a.autoSelected++
				}
				return a, nil
			case "tab", "enter":
				if a.autoSelected < len(a.autoItems) {
					chosen := a.autoItems[a.autoSelected]
					a.textarea.SetValue(chosen.name + " ")
					a.autoVisible = false
					// If enter on a no-arg command, execute immediately.
					if msg.String() == "enter" {
						return a.handleSlashCommand(chosen.name)
					}
				}
				return a, nil
			case "esc":
				a.autoVisible = false
				return a, nil
			}
		}

		switch msg.String() {
		case "ctrl+d":
			if a.textarea.Value() == "" {
				a.autoSave()
				return a, tea.Quit
			}

		case "ctrl+l":
			a.ag.ClearHistory()
			a.conv = nil
			a.refreshViewport()
			a.addEntry("assistant", "Conversation cleared.")
			a.refreshViewport()
			return a, nil

		case "ctrl+up":
			a.resizeInput(1)
			return a, nil

		case "ctrl+down":
			a.resizeInput(-1)
			return a, nil

		case "pgup":
			a.viewport.HalfPageUp()
			return a, nil

		case "pgdown":
			a.viewport.HalfPageDown()
			return a, nil

		case "up":
			if a.textarea.Value() == "" {
				a.viewport.ScrollUp(1)
				return a, nil
			}

		case "down":
			if a.textarea.Value() == "" {
				a.viewport.ScrollDown(1)
				return a, nil
			}

		case "enter":
			input := strings.TrimSpace(a.textarea.Value())
			if input == "" {
				return a, nil
			}

			// Handle slash commands.
			if strings.HasPrefix(input, "/") {
				return a.handleSlashCommand(input)
			}

			a.textarea.Reset()
			a.autoVisible = false
			return a, a.sendNewMessage(input)
		}
	}

	// Pass key to textarea in chat mode, then update autocomplete.
	if a.state == stateChat {
		var cmd tea.Cmd
		a.textarea, cmd = a.textarea.Update(msg)
		a.updateAutocomplete()
		return a, cmd
	}
	return a, nil
}

func (a *App) updateAutocomplete() {
	input := strings.TrimSpace(a.textarea.Value())
	if !strings.HasPrefix(input, "/") {
		a.autoVisible = false
		return
	}
	a.autoItems = nil
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, input) {
			a.autoItems = append(a.autoItems, c)
		}
	}
	if len(a.autoItems) == 0 || (len(a.autoItems) == 1 && a.autoItems[0].name == input) {
		a.autoVisible = false
		return
	}
	if a.autoSelected >= len(a.autoItems) {
		a.autoSelected = 0
	}
	a.autoVisible = true
}

func (a *App) renderAutocomplete() string {
	if !a.autoVisible || len(a.autoItems) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, item := range a.autoItems {
		if i == a.autoSelected {
			sb.WriteString(styleAutoSelected.Render(fmt.Sprintf("  %s — %s", item.name, item.desc)))
		} else {
			sb.WriteString(styleAutoItem.Render(fmt.Sprintf("  %s — %s", item.name, item.desc)))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (a *App) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	a.autoVisible = false
	parts := strings.Fields(input)
	cmd := parts[0]
	switch cmd {
	case "/model":
		a.textarea.Reset()
		a.state = statePickModel
		return a, a.loadModels()
	case "/clear":
		a.ag.ClearHistory()
		a.conv = nil
		a.sessionID = ""
		a.refreshViewport()
		a.addEntry("assistant", "Conversation cleared.")
		a.refreshViewport()
		a.textarea.Reset()
	case "/help":
		a.addEntry("assistant", helpText())
		a.refreshViewport()
		a.viewport.GotoBottom()
		a.textarea.Reset()
	case "/compact":
		a.textarea.Reset()
		return a, a.cmdCompact()
	case "/save":
		a.cmdSave(parts)
		a.textarea.Reset()
	case "/load":
		a.cmdLoad(parts)
		a.textarea.Reset()
	case "/exit", "/quit":
		a.autoSave()
		return a, tea.Quit
	case "/history":
		a.cmdHistory()
		a.textarea.Reset()
	case "/delete":
		a.cmdDelete(parts)
		a.textarea.Reset()
	default:
		a.addEntry("error", fmt.Sprintf("Unknown command: %s\nType /help for available commands.", cmd))
		a.refreshViewport()
		a.textarea.Reset()
	}
	return a, nil
}

// cmdCompact starts the compact operation. It runs the summarization in a
// sendMessage-style Cmd, streaming the summary and replacing history on completion.
func (a *App) cmdCompact() tea.Cmd {
	if len(a.ag.Messages()) == 0 {
		a.addEntry("error", "Nothing to compact.")
		a.refreshViewport()
		return nil
	}

	a.addEntry("assistant", "*Compacting conversation...*\n\n")
	a.state = stateStreaming

	ctx, cancel := context.WithCancel(context.Background())
	a.cancelStream = cancel

	return tea.Batch(a.spinner.Tick, func() tea.Msg {
		tokenCb := func(token string) {
			a.streamCh <- StreamTokenMsg{Token: token}
		}
		err := a.ag.Compact(ctx, tokenCb)
		if err != nil && ctx.Err() == nil {
			a.streamCh <- StreamErrMsg{Err: err}
		} else {
			a.streamCh <- StreamDoneMsg{}
		}
		return nil
	}, a.waitForStreamMsg())
}

// autoSave persists the current conversation. Creates a new session on first save,
// updates the existing one on subsequent saves.
// fetchModelContext fetches the model's context length from Ollama asynchronously.
func (a *App) fetchModelContext(model string) tea.Cmd {
	return func() tea.Msg {
		info, err := a.ollamaClient.ModelInfo(context.Background(), model)
		if err != nil {
			return ModelContextMsg{ContextLength: 0}
		}
		return ModelContextMsg{ContextLength: info.ContextLength()}
	}
}

// fetchModelContextSync fetches synchronously (for use in non-cmd paths like loadSession).
func (a *App) fetchModelContextSync(model string) {
	info, err := a.ollamaClient.ModelInfo(context.Background(), model)
	if err != nil || info.ContextLength() == 0 {
		a.modelContextLen = a.cfg.ContextLen
		a.ag.ModelContextLen = a.cfg.ContextLen
	} else {
		a.modelContextLen = info.ContextLength()
		a.ag.ModelContextLen = info.ContextLength()
	}
}

func (a *App) autoSave() {
	msgs := a.ag.Messages()
	if len(msgs) == 0 {
		return
	}

	if a.sessionID != "" {
		if err := history.Update(a.sessionID, msgs); err != nil {
			a.addEntry("error", fmt.Sprintf("Auto-save failed: %v", err))
			a.refreshViewport()
		}
		return
	}

	id, err := history.Save(a.ag.Model(), msgs, autoName(msgs))
	if err != nil {
		a.addEntry("error", fmt.Sprintf("Auto-save failed: %v", err))
		a.refreshViewport()
		return
	}
	a.sessionID = id
}

func (a *App) cmdSave(parts []string) {
	msgs := a.ag.Messages()
	if len(msgs) == 0 {
		a.addEntry("error", "Nothing to save.")
		a.refreshViewport()
		return
	}

	name := ""
	if len(parts) > 1 {
		name = strings.Join(parts[1:], " ")
	}

	if a.sessionID != "" {
		if name != "" {
			if err := history.Rename(a.sessionID, name); err != nil {
				a.addEntry("error", fmt.Sprintf("Rename failed: %v", err))
				a.refreshViewport()
				return
			}
		}
		if err := history.Update(a.sessionID, msgs); err != nil {
			a.addEntry("error", fmt.Sprintf("Save failed: %v", err))
			a.refreshViewport()
			return
		}
		a.addEntry("assistant", fmt.Sprintf("Session **%s** updated.", a.sessionID))
	} else {
		if name == "" {
			name = autoName(msgs)
		}
		id, err := history.Save(a.ag.Model(), msgs, name)
		if err != nil {
			a.addEntry("error", fmt.Sprintf("Save failed: %v", err))
			a.refreshViewport()
			return
		}
		a.sessionID = id
		a.addEntry("assistant", fmt.Sprintf("Session saved as **%s** (%s).", id, name))
	}
	a.refreshViewport()
	a.viewport.GotoBottom()
}

func (a *App) cmdLoad(parts []string) {
	if len(parts) < 2 {
		a.addEntry("error", "Usage: /load <name-or-id>\nUse /history to list sessions.")
		a.refreshViewport()
		return
	}
	query := strings.Join(parts[1:], " ")

	s, err := history.Load(query)
	if err != nil {
		a.addEntry("error", fmt.Sprintf("Load failed: %v", err))
		a.refreshViewport()
		return
	}

	a.ag.SetMessages(s.Messages)
	a.ag.SetModel(s.Model)
	a.sessionID = s.ID
	a.conv = nil

	// Rebuild display entries from loaded messages.
	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			a.addEntry("user", m.Content)
		case "assistant":
			a.addEntry("assistant", m.Content)
		case "tool":
			a.addEntry("tool", m.Content)
		}
	}

	label := s.ID
	if s.Name != "" {
		label = s.Name
	}
	a.addEntry("assistant", fmt.Sprintf("Loaded session **%s** (%d messages, model: %s).", label, len(s.Messages), s.Model))
	a.refreshViewport()
	a.viewport.GotoBottom()
}

func (a *App) cmdDelete(parts []string) {
	if len(parts) < 2 {
		a.addEntry("error", "Usage: /delete <name-or-id>\nUse /history to list sessions.")
		a.refreshViewport()
		return
	}
	query := strings.Join(parts[1:], " ")

	s, err := history.Load(query)
	if err != nil {
		a.addEntry("error", fmt.Sprintf("Delete failed: %v", err))
		a.refreshViewport()
		return
	}

	if err := history.Delete(s.ID); err != nil {
		a.addEntry("error", fmt.Sprintf("Delete failed: %v", err))
		a.refreshViewport()
		return
	}

	label := s.ID
	if s.Name != "" {
		label = s.Name
	}
	if a.sessionID == s.ID {
		a.sessionID = ""
	}
	a.addEntry("assistant", fmt.Sprintf("Deleted session **%s**.", label))
	a.refreshViewport()
	a.viewport.GotoBottom()
}

func (a *App) cmdHistory() {
	sessions, err := history.List()
	if err != nil {
		a.addEntry("error", fmt.Sprintf("Failed to list history: %v", err))
		a.refreshViewport()
		return
	}

	if len(sessions) == 0 {
		a.addEntry("assistant", "No saved sessions. Use /save to save the current conversation.")
		a.refreshViewport()
		a.viewport.GotoBottom()
		return
	}

	a.hpicker = newHistoryPicker(sessions, a.width, a.height-4)
	a.state = stateHistory
}

func (a *App) loadSession(s *history.Session) {
	a.ag.SetMessages(s.Messages)
	a.ag.SetModel(s.Model)
	a.sessionID = s.ID
	a.conv = nil

	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			a.addEntry("user", m.Content)
		case "assistant":
			a.addEntry("assistant", m.Content)
		case "tool":
			a.addEntry("tool", m.Content)
		}
	}

	label := s.ID
	if s.Name != "" {
		label = s.Name
	}
	a.addEntry("assistant", fmt.Sprintf("Loaded session **%s** (%d messages, model: %s).", label, len(s.Messages), s.Model))
	a.refreshViewport()
	a.viewport.GotoBottom()
	a.state = stateChat
	a.textarea.Focus()
	a.fetchModelContextSync(s.Model)
}

// autoName generates a session name from the first user message.
func autoName(msgs []ollama.Message) string {
	for _, m := range msgs {
		if m.Role == "user" && m.Content != "" {
			name := strings.ReplaceAll(m.Content, "\n", " ")
			if len(name) > 40 {
				name = name[:40] + "..."
			}
			return name
		}
	}
	return ""
}

func (a *App) sendMessage(ctx context.Context, input string) tea.Cmd {
	return func() tea.Msg {
		tokenCb := func(token string) {
			a.streamCh <- StreamTokenMsg{Token: token}
		}

		toolEvent := func(name, args, result string) {
			a.streamCh <- ToolEventMsg{Name: name, Args: args, Result: result}
		}

		approval := func(toolName, args string) bool {
			if a.cfg != nil && a.cfg.AutoApprove[toolName] {
				return true
			}
			ch := make(chan bool, 1)
			a.streamCh <- ApprovalRequestMsg{ToolName: toolName, Args: args, Response: ch}
			return <-ch
		}
		a.ag.SetApproval(approval)

		err := a.ag.Send(ctx, input, tokenCb, toolEvent)
		if err != nil && ctx.Err() == nil {
			a.streamCh <- StreamErrMsg{Err: err}
		} else {
			a.streamCh <- StreamDoneMsg{PromptTokens: a.ag.LastPromptTokens}
		}
		// All messages routed via channel; nothing to return to the event loop.
		return nil
	}
}

// sendNewMessage adds a user entry, transitions to streaming, and starts the agent.
func (a *App) sendNewMessage(input string) tea.Cmd {
	a.pendingMsgs = nil
	a.addEntry("user", input)
	a.refreshViewport()
	a.viewport.GotoBottom()
	a.state = stateStreaming
	a.textarea.Placeholder = "Message... (Enter send, Alt+Enter newline)"

	ctx, cancel := context.WithCancel(context.Background())
	a.cancelStream = cancel

	return tea.Batch(a.spinner.Tick, a.sendMessage(ctx, input), a.waitForStreamMsg())
}

// sendQueuedMessage sends a previously queued message without clearing the queue.
func (a *App) sendQueuedMessage(input string) tea.Cmd {
	a.state = stateStreaming
	if len(a.pendingMsgs) > 0 {
		a.textarea.Placeholder = fmt.Sprintf("%d message(s) queued...", len(a.pendingMsgs))
	} else {
		a.textarea.Placeholder = "Message... (Enter send, Alt+Enter newline)"
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancelStream = cancel

	return tea.Batch(a.spinner.Tick, a.sendMessage(ctx, input), a.waitForStreamMsg())
}

// finalizeStreaming converts the last streaming entry to a completed one
// so it gets rendered with glamour instead of raw text.
func (a *App) finalizeStreaming() {
	if len(a.conv) > 0 && a.conv[len(a.conv)-1].role == "assistant-stream" {
		a.conv[len(a.conv)-1].role = "assistant"
	}
}

// View renders the entire UI.
func (a *App) View() string {
	if a.width == 0 {
		return "Initializing..."
	}

	switch a.state {
	case stateLoading:
		return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center,
			a.spinner.View()+" Connecting to Ollama...")

	case statePickModel:
		return a.picker.View()

	case stateApproval:
		// Show chat + approval modal overlay.
		chat := a.renderChat()
		modal := a.approval.View()
		return lipgloss.JoinVertical(lipgloss.Left, chat, modal)

	case stateHistory:
		return a.hpicker.View()
	}

	return a.renderChat()
}

func (a *App) renderChat() string {
	header := a.renderHeader()
	inputArea := a.renderInput()
	autoComplete := a.renderAutocomplete()

	// Calculate viewport height.
	headerHeight := lipgloss.Height(header)
	inputHeight := lipgloss.Height(inputArea) + lipgloss.Height(autoComplete)
	helpHeight := 1
	a.viewport.Height = a.height - headerHeight - inputHeight - helpHeight - 2

	help := styleHelp.Render("/exit quit • ↑↓/pgup/pgdn scroll • ctrl+↑↓ resize input • alt+enter newline • ⌘C copy")

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		a.viewport.View(),
		inputArea,
		autoComplete,
		help,
	)
}

func (a *App) renderHeader() string {
	model := a.ag.Model()
	if model == "" {
		model = "no model"
	}
	left := styleHeader.Render(" cli-agent ")

	// Build right side: model + optional context usage.
	rightParts := []string{styleModel.Render("[" + model + "]")}
	if a.lastPromptTokens > 0 && a.modelContextLen > 0 {
		ctxLabel := fmt.Sprintf("ctx:%.0fk/%.0fk", float64(a.lastPromptTokens)/1024, float64(a.modelContextLen)/1024)
		pct := float64(a.lastPromptTokens) / float64(a.modelContextLen)
		if pct > 0.8 {
			rightParts = append(rightParts, styleError.Render(ctxLabel))
		} else {
			rightParts = append(rightParts, styleMuted.Render(ctxLabel))
		}
	}
	right := " " + strings.Join(rightParts, " ") + " "

	gap := strings.Repeat(" ", max(0, a.width-lipgloss.Width(left)-lipgloss.Width(right)))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
}

func (a *App) renderInput() string {
	var prefix string
	if a.state == stateStreaming {
		prefix = a.spinner.View() + " "
	} else {
		prefix = styleUserPrefix.Render("You: ")
	}
	inputView := styleInputBorder.Width(a.width - 4).Render(a.textarea.View())
	return prefix + "\n" + inputView
}

func (a *App) addEntry(role, content string) {
	a.conv = append(a.conv, chatEntry{role: role, content: content})
}

// appendToken appends a token to the last assistant entry (for streaming).
func (a *App) appendToken(token string) {
	if len(a.conv) > 0 && a.conv[len(a.conv)-1].role == "assistant-stream" {
		a.conv[len(a.conv)-1].content += token
	} else {
		a.conv = append(a.conv, chatEntry{role: "assistant-stream", content: token})
	}
	a.refreshViewport()
}

func (a *App) refreshViewport() {
	var sb strings.Builder
	for i, entry := range a.conv {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch entry.role {
		case "user":
			sb.WriteString(styleUserPrefix.Render("You: "))
			sb.WriteString(entry.content)
		case "assistant":
			sb.WriteString(styleAssistantPrefix.Render("Assistant: "))
			rendered, err := a.renderer.Render(entry.content)
			if err != nil {
				sb.WriteString(entry.content)
			} else {
				sb.WriteString(strings.TrimRight(rendered, "\n"))
			}
		case "assistant-stream":
			sb.WriteString(styleAssistantPrefix.Render("Assistant: "))
			sb.WriteString(entry.content)
			sb.WriteString("◌")
		case "tool":
			sb.WriteString(styleToolName.Render("▶ Tool: "))
			sb.WriteString(styleToolResult.Render(entry.content))
		case "error":
			sb.WriteString(styleError.Render("✗ " + entry.content))
		}
		sb.WriteString("\n")
	}
	a.viewport.SetContent(sb.String())
}

func (a *App) resizeComponents() {
	a.textarea.SetWidth(a.width - 6)
	a.textarea.SetHeight(a.inputHeight)
	a.viewport.Width = a.width
	if a.pickerReady {
		a.picker.resize(a.width, a.height-4)
	}
	if a.state == stateHistory {
		a.hpicker.resize(a.width, a.height-4)
	}
	a.approval.width = a.width
	a.viewport.Height = a.height - 10
}

func (a *App) resizeInput(delta int) {
	newH := a.inputHeight + delta
	newH = max(1, min(newH, a.height/2))
	if newH == a.inputHeight {
		return
	}
	a.inputHeight = newH
	a.textarea.SetHeight(a.inputHeight)
}

func helpText() string {
	return `**Available commands:**

| Command | Description |
|---|---|
| /model | Switch the active model |
| /clear | Clear conversation history |
| /compact | Summarize conversation to free context |
| /save [name] | Save session (optional friendly name) |
| /load <name> | Load a saved session |
| /history | List saved sessions |
| /delete <name> | Delete a saved session |
| /exit, /quit | Quit (auto-saves) |
| /help | Show this help |

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| Enter | Send message |
| Alt+Enter | New line in input |
| Ctrl+D | Quit when input empty (auto-saves) |
| Ctrl+C | Cancel streaming |
| Ctrl+L | Clear conversation |
| PageUp/PageDown | Scroll chat history |

**Available tools:** read\_file, write\_file, list\_dir, find\_files, run\_shell, fetch\_url, web\_search`
}

// Run starts the Bubbletea program.
func Run(client *ollama.Client, ag *agent.Agent, cfg *config.Config) error {
	app := New(client, ag, cfg)
	p := tea.NewProgram(app, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

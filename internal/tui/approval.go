package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type approvalModal struct {
	toolName string
	args     string
	response chan bool
	width    int
}

type approvalResultMsg struct {
	allowed  bool
	response chan bool
}

func (m approvalModal) Update(msg tea.Msg) (approvalModal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch strings.ToLower(msg.String()) {
		case "y", "enter":
			return m, func() tea.Msg {
				return approvalResultMsg{allowed: true, response: m.response}
			}
		case "n", "esc", "ctrl+c":
			return m, func() tea.Msg {
				return approvalResultMsg{allowed: false, response: m.response}
			}
		}
	}
	return m, nil
}

func (m approvalModal) View() string {
	title := styleApprovalTitle.Render("  Tool requires approval  ")

	args := m.args
	if len(args) > 300 {
		args = args[:300] + "..."
	}

	body := fmt.Sprintf(
		"%s\n\nTool:      %s\nArguments:\n%s",
		title,
		styleToolName.Render(m.toolName),
		styleMuted.Render(args),
	)

	actions := fmt.Sprintf("\n  %s  %s",
		styleApprovalAllow.Render("[Y] Allow"),
		styleApprovalDeny.Render("[N] Deny"),
	)

	content := body + actions

	// Center in available width.
	w := max(m.width-4, 40)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorWarning).
		Padding(1, 2).
		Width(w).
		Render(content)

	return lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, box)
}

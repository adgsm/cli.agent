package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary  = lipgloss.Color("#7C3AED") // purple
	colorAccent   = lipgloss.Color("#10B981") // green
	colorMuted    = lipgloss.Color("#6B7280")
	colorError    = lipgloss.Color("#EF4444")
	colorWarning  = lipgloss.Color("#F59E0B")
	colorUserMsg  = lipgloss.Color("#60A5FA") // blue
	colorBorder   = lipgloss.Color("#374151")

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F9FAFB")).
			Background(colorPrimary).
			Padding(0, 1)

	styleModel = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	styleUserPrefix = lipgloss.NewStyle().
			Foreground(colorUserMsg).
			Bold(true)

	styleAssistantPrefix = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	styleToolName = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true)

	styleToolResult = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	styleError = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	styleApprovalTitle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorWarning)

	styleApprovalAllow = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent).
				Padding(0, 1)

	styleApprovalDeny = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorError).
				Padding(0, 1)

	styleInputBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPrimary).
				Padding(0, 1)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	styleAutoSelected = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F9FAFB")).
				Background(colorPrimary).
				Padding(0, 1)

	styleAutoItem = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF")).
			Padding(0, 1)
)

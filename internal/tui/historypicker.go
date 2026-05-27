package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mdzunic/cli-agent/internal/history"
)

type historyItem struct {
	session *history.Session
}

func (h historyItem) Title() string {
	label := h.session.Name
	if label == "" {
		label = h.session.ID
	}
	return label
}
func (h historyItem) Description() string {
	return fmt.Sprintf("%s • %d msgs • %s", h.session.UpdatedAt.Format("Jan 02 15:04"), len(h.session.Messages), h.session.Model)
}
func (h historyItem) FilterValue() string { return h.session.Name + " " + h.session.ID }

type historyPicker struct {
	list list.Model
}

func newHistoryPicker(sessions []history.Session, width, height int) historyPicker {
	items := make([]list.Item, len(sessions))
	for i := range sessions {
		items[i] = historyItem{session: &sessions[i]}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#7C3AED")).
		BorderLeftForeground(lipgloss.Color("#7C3AED"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("#A78BFA"))

	l := list.New(items, delegate, width, height)
	l.Title = "Saved Sessions"
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F9FAFB")).
		Background(lipgloss.Color("#7C3AED")).Padding(0, 1)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		}
	}

	return historyPicker{list: l}
}

func (h historyPicker) Update(msg tea.Msg) (historyPicker, tea.Cmd) {
	var cmd tea.Cmd
	h.list, cmd = h.list.Update(msg)
	return h, cmd
}

func (h historyPicker) View() string {
	return h.list.View()
}

func (h *historyPicker) selectedSession() *history.Session {
	if item, ok := h.list.SelectedItem().(historyItem); ok {
		return item.session
	}
	return nil
}

func (h *historyPicker) resize(width, height int) {
	h.list.SetSize(width, height)
}

func (h *historyPicker) removeItem(id string) {
	var items []list.Item
	for _, item := range h.list.Items() {
		if hi, ok := item.(historyItem); ok && hi.session.ID == id {
			continue
		}
		items = append(items, item)
	}
	h.list.SetItems(items)
}

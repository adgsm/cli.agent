package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type modelItem struct{ name string }

func (m modelItem) Title() string       { return m.name }
func (m modelItem) Description() string { return "" }
func (m modelItem) FilterValue() string { return m.name }

type modelPicker struct {
	list   list.Model
	chosen string
}

func newModelPicker(models []string, width, height int) modelPicker {
	items := make([]list.Item, len(models))
	for i, m := range models {
		items[i] = modelItem{name: m}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#7C3AED")).
		BorderLeftForeground(lipgloss.Color("#7C3AED"))

	l := list.New(items, delegate, width, height)
	l.Title = "Select a model"
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F9FAFB")).
		Background(lipgloss.Color("#7C3AED")).Padding(0, 1)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	return modelPicker{list: l}
}

func (m modelPicker) Update(msg tea.Msg) (modelPicker, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m modelPicker) View() string {
	return m.list.View()
}

func (m *modelPicker) selectedModel() string {
	if item, ok := m.list.SelectedItem().(modelItem); ok {
		return item.name
	}
	return ""
}

func (m *modelPicker) resize(width, height int) {
	m.list.SetSize(width, height)
}

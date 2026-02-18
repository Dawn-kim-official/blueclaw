package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleYouLabel      = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	styleBlueclawLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleErrorLabel    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	styleErrorText     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	stylePromptIcon    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleDivider       = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	styleStatus        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	styleThinking      = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
	styleOutboxLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

type chatEntry struct {
	role    string
	source  string
	content string
}

type chatResponseMsg struct {
	sessionID string
	content   string
	err       error
}

type outboxCheckMsg struct {
	entries []outboxEntry
}

type outboxEntry struct {
	source  string
	content string
}

type replModel struct {
	viewport           viewport.Model
	textinput          textinput.Model
	history            []chatEntry
	sessionID          string
	sending            bool
	ready              bool
	width              int
	height             int
	outboxPollInterval time.Duration
}

func newReplModel(outboxPollInterval time.Duration) replModel {
	input := textinput.New()
	input.Placeholder = "Message Blueclaw..."
	input.Focus()
	input.CharLimit = 0
	input.Prompt = ""
	return replModel{
		textinput:          input,
		outboxPollInterval: outboxPollInterval,
	}
}

func (model replModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		pollOutboxCmd(model.outboxPollInterval),
	)
}

func pollOutboxCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(_ time.Time) tea.Msg {
		return outboxCheckMsg{entries: collectOutboxMessages()}
	})
}

func sendChatCmd(input, sessionID string) tea.Cmd {
	return func() tea.Msg {
		response, err := sendChatMessage(input, sessionID)
		if err != nil {
			return chatResponseMsg{err: err}
		}
		return chatResponseMsg{sessionID: response.SessionID, content: response.Response}
	}
}

func (model replModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width = message.Width
		model.height = message.Height
		model = model.resize()
	case tea.KeyMsg:
		switch message.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD:
			return model, tea.Quit
		case tea.KeyEnter:
			return model.handleSend()
		}
	case chatResponseMsg:
		model.sending = false
		if message.err != nil {
			model.history = append(model.history, chatEntry{role: "error", content: message.err.Error()})
		} else {
			model.history = append(model.history, chatEntry{role: "assistant", content: message.content})
			model.sessionID = message.sessionID
		}
		model.viewport.SetContent(model.renderHistory())
		model.viewport.GotoBottom()
	case outboxCheckMsg:
		for _, entry := range message.entries {
			model.history = append(model.history, chatEntry{role: "outbox", source: entry.source, content: entry.content})
		}
		if len(message.entries) > 0 {
			model.viewport.SetContent(model.renderHistory())
			model.viewport.GotoBottom()
		}
		commands = append(commands, pollOutboxCmd(model.outboxPollInterval))
	}
	var inputCmd tea.Cmd
	model.textinput, inputCmd = model.textinput.Update(message)
	commands = append(commands, inputCmd)
	var viewportCmd tea.Cmd
	model.viewport, viewportCmd = model.viewport.Update(message)
	commands = append(commands, viewportCmd)
	return model, tea.Batch(commands...)
}

func (model replModel) handleSend() (tea.Model, tea.Cmd) {
	if model.sending {
		return model, nil
	}
	input := strings.TrimSpace(model.textinput.Value())
	if input == "" {
		return model, nil
	}
	model.history = append(model.history, chatEntry{role: "user", content: input})
	model.textinput.Reset()
	model.sending = true
	model.viewport.SetContent(model.renderHistory())
	model.viewport.GotoBottom()
	return model, sendChatCmd(input, model.sessionID)
}

func (model replModel) resize() replModel {
	inputAreaHeight := 3
	viewportHeight := model.height - inputAreaHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	if !model.ready {
		model.viewport = viewport.New(model.width, viewportHeight)
		model.ready = true
	} else {
		model.viewport.Width = model.width
		model.viewport.Height = viewportHeight
	}
	model.textinput.Width = model.width - 4
	model.viewport.SetContent(model.renderHistory())
	return model
}

func (model replModel) renderHistory() string {
	if len(model.history) == 0 && !model.sending {
		return styleStatus.Render("No conversation yet. Start typing below.")
	}
	lines := make([]string, 0, len(model.history)*2)
	for _, entry := range model.history {
		lines = append(lines, model.renderEntry(entry))
	}
	if model.sending {
		lines = append(lines, styleThinking.Render("Blueclaw is thinking..."))
	}
	return strings.Join(lines, "\n")
}

func (model replModel) renderEntry(entry chatEntry) string {
	switch entry.role {
	case "user":
		return styleYouLabel.Render("You") + "  " + entry.content
	case "assistant":
		return styleBlueclawLabel.Render("Blueclaw") + "  " + entry.content
	case "outbox":
		label := styleOutboxLabel.Render(entry.source)
		return label + "  " + entry.content
	case "error":
		return styleErrorLabel.Render("Error") + "  " + styleErrorText.Render(entry.content)
	}
	return entry.content
}

func (model replModel) View() string {
	if !model.ready {
		return "Starting Blueclaw...\n"
	}
	divider := styleDivider.Render(strings.Repeat("─", model.width))
	prompt := stylePromptIcon.Render("❯") + " " + model.textinput.View()
	status := model.statusLine()
	return fmt.Sprintf("%s\n%s\n%s\n%s", model.viewport.View(), divider, prompt, status)
}

func (model replModel) statusLine() string {
	if model.sending {
		return styleThinking.Render("sending...")
	}
	return styleStatus.Render("enter  send   ctrl+d  quit   ↑↓  scroll history")
}

func startREPL(outboxPollInterval time.Duration) error {
	program := tea.NewProgram(
		newReplModel(outboxPollInterval),
		tea.WithAltScreen(),
	)
	_, err := program.Run()
	return err
}

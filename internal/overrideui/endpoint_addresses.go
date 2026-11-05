package overrideui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/omry/reploy/internal/deploy"
)

func (m *model) endpointNames() []string {
	var names []string
	if m.document.Docker.Workload != nil {
		for name := range m.document.Docker.Workload.Endpoints {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
func (m *model) endpointName() string {
	names := m.endpointNames()
	if len(names) == 0 {
		return ""
	}
	return names[clamp(m.endpointCursor, 0, len(names)-1)]
}
func (m *model) endpointSummary() string {
	addresses, err := deploy.ResolvePublishedAddressesV1(m.document, m.publishedAddresses)
	if err != nil {
		return "Invalid endpoint overrides: " + sanitizeTerminalText(err.Error()) + "\n"
	}
	var body strings.Builder
	for index, name := range m.endpointNames() {
		source := "From blueprint"
		if _, exists := m.publishedAddresses[name]; exists {
			source = "Explicit override"
		}
		endpoint := m.document.Docker.Workload.Endpoints[name]
		line := fmt.Sprintf("%s: %s · %s · staging %d / deployed %d", name, addresses[name], source, endpoint.Publish.Staging, endpoint.Publish.Deployed)
		line = sanitizeTerminalText(line)
		if m.screen == screenEndpoints && index == m.endpointCursor {
			line = focusStyle.Render(line)
		}
		body.WriteString(line + "\n")
		if addresses[name] == "0.0.0.0" {
			body.WriteString("  Publishes on all IPv4 interfaces.\n")
		}
	}
	return body.String()
}
func (m *model) viewEndpoints() string {
	return panelStyle.Width(m.dialogWidth()).Render(titleStyle.Render("Endpoint published addresses") + "\n\n" + m.endpointSummary() + "\n↑/↓ choose · Enter edit · D reset to From blueprint · Esc back\nSaving does not start or restart workloads.")
}
func (m *model) updateEndpoints(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.screen = screenMain
	case "up":
		m.endpointCursor = clamp(m.endpointCursor-1, 0, len(m.endpointNames())-1)
	case "down":
		m.endpointCursor = clamp(m.endpointCursor+1, 0, len(m.endpointNames())-1)
	case "d", "D", "delete", "backspace":
		m.setPublishedAddress(m.endpointName(), "")
	case "enter", "e", "E":
		addresses, _ := deploy.ResolvePublishedAddressesV1(m.document, m.publishedAddresses)
		m.input.SetValue(addresses[m.endpointName()])
		m.input.CursorEnd()
		m.input.Focus()
		m.screen = screenEndpointAddress
	}
	return m, nil
}
func (m *model) updateEndpointAddress(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.screen = screenEndpoints
		return m, nil
	case "enter":
		value := m.input.Value()
		if err := deploy.ValidatePublishedAddressV1(value); err != nil {
			m.status = "Invalid published address: " + err.Error()
			return m, nil
		}
		m.setPublishedAddress(m.endpointName(), value)
		m.input.Blur()
		m.screen = screenEndpoints
		return m, nil
	}
	var command tea.Cmd
	m.input, command = m.input.Update(key)
	return m, command
}
func (m *model) setPublishedAddress(name, address string) {
	if m.publishedAddresses[name] == address {
		return
	}
	if address == "" {
		delete(m.publishedAddresses, name)
	} else {
		if m.publishedAddresses == nil {
			m.publishedAddresses = map[string]string{}
		}
		m.publishedAddresses[name] = address
	}
	m.dirty = true
	m.invalidateValidation()
}

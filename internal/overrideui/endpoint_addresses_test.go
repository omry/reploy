package overrideui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

func TestEndpointEditorSaveReopenResetAndConcurrentRefusal(t *testing.T) {
	dir := stagedEditorDir(t)
	document := editorDocument("demo")
	document.Docker.Workload = &blueprint.DockerWorkload{Endpoints: map[string]blueprint.DockerEndpoint{
		"https":   {Publish: blueprint.Publication{Address: "127.0.0.1", Staging: 18075, Deployed: 8075}},
		"metrics": {Publish: blueprint.Publication{Address: "127.0.0.2", Staging: 19090, Deployed: 9090}},
	}}
	writeEditorState(t, dir, document)
	raw := deploy.EmptyPackageOverridesV1("example")
	raw.Environment.Vars["note"] = "retained"
	raw.Environment.Base = &deploy.BaseImageOverrideV1{Image: "python:3.13-slim"}
	raw.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{"demo": {Version: "1.2.3"}}
	commitOverrides(t, dir, raw)
	statePath := filepath.Join(dir, ".reploy", "state.json")
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	open := func() *model {
		t.Helper()
		m, err := newModel(Config{Context: t.Context(), DeploymentDir: dir, Document: document})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := open()
	for _, want := range []string{"https", "127.0.0.1", "From blueprint", "18075", "8075"} {
		if !strings.Contains(m.viewMain(), want) {
			t.Fatalf("main missing %s", want)
		}
	}
	m.updateMain(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.screen != screenEndpoints {
		t.Fatal("N did not open endpoints")
	}
	m.updateEndpoints(tea.KeyMsg{Type: tea.KeyEnter})
	m.input.SetValue("0.0.0.0")
	m.updateEndpointAddress(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dirty || !strings.Contains(m.viewEndpoints(), "all IPv4 interfaces") || !strings.Contains(m.viewEndpoints(), "Explicit override") {
		t.Fatal("explicit value not visible")
	}
	draft := m.buildRaw()
	draft.Docker = deploy.DockerOverridesV1{}
	originalEncoded, _ := deploy.EncodePackageOverridesV1(raw)
	draftEncoded, _ := deploy.EncodePackageOverridesV1(draft)
	if !bytes.Equal(originalEncoded, draftEncoded) {
		t.Fatalf("address edit changed unrelated overrides: %#v", m.buildRaw())
	}
	saved, err := saveSidecarAt(t.Context(), dir, m.original, m.buildRaw())
	if err != nil {
		t.Fatal(err)
	}
	stateAfter, _ := os.ReadFile(statePath)
	if string(stateBefore) != string(stateAfter) {
		t.Fatal("save changed desired/build state")
	}
	reopened := open()
	if reopened.publishedAddresses["https"] != "0.0.0.0" || len(reopened.publishedAddresses) != 1 {
		t.Fatalf("reopen=%v", reopened.publishedAddresses)
	}
	// Cancel an in-progress edit without modifying either the draft or saved file.
	reopened.screen = screenEndpoints
	reopened.updateEndpoints(tea.KeyMsg{Type: tea.KeyEnter})
	reopened.input.SetValue("192.0.2.42")
	reopened.updateEndpointAddress(tea.KeyMsg{Type: tea.KeyEsc})
	if reopened.publishedAddresses["https"] != "0.0.0.0" || reopened.dirty {
		t.Fatal("cancel applied an edit")
	}
	reopened.updateEndpoints(tea.KeyMsg{Type: tea.KeyEnter})
	reopened.input.SetValue("")
	reopened.updateEndpointAddress(tea.KeyMsg{Type: tea.KeyEnter})
	if reopened.screen != screenEndpointAddress || !strings.Contains(reopened.status, "Invalid") {
		t.Fatal("empty address accepted")
	}
	reopened.updateEndpointAddress(tea.KeyMsg{Type: tea.KeyEsc})
	reopened.updateEndpoints(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if len(reopened.publishedAddresses) != 0 {
		t.Fatal("reset retained explicit address")
	}
	if _, err := saveSidecarAt(t.Context(), dir, saved, reopened.buildRaw()); err != nil {
		t.Fatal(err)
	}
	if len(open().publishedAddresses) != 0 {
		t.Fatal("reset not saved")
	}
	if _, err := saveSidecarAt(t.Context(), dir, saved, m.buildRaw()); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("concurrent change accepted: %v", err)
	}
	loaded, _, err := deploy.ReadPackageOverridesV1(dir)
	loadedEncoded, _ := deploy.EncodePackageOverridesV1(loaded)
	if err != nil || !bytes.Equal(loadedEncoded, originalEncoded) {
		t.Fatalf("reset lost unrelated values: %#v %v", loaded, err)
	}
}

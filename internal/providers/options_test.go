package providers

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestComponentOptionsAndSelection(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{Components: map[string]blueprint.Component{
		"application": {
			Type:   blueprint.ComponentTypePython,
			Python: &blueprint.PythonComponent{Requirements: []string{"demo"}},
			Options: map[string]blueprint.ComponentOption{
				"smtp": {Description: "SMTP", PythonRequirements: []string{"demo-smtp"}},
				"imap": {Description: "IMAP", PythonRequirements: []string{"demo-imap"}},
			},
		},
	}}}
	options := ComponentOptions(document)
	if got := []string{options[0].Name, options[1].Name}; !reflect.DeepEqual(got, []string{"imap", "smtp"}) {
		t.Fatalf("options = %#v", got)
	}
	selected, err := SelectComponents(document, []string{"smtp"}, []string{"imap"}, []string{"smtp"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"imap"}) {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectComponentsDoesNotTreatRequiredComponentAsOption(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{Components: map[string]blueprint.Component{
		"application": {Type: blueprint.ComponentTypePython, Python: &blueprint.PythonComponent{Requirements: []string{"demo"}}},
	}}}
	if _, err := SelectComponents(document, nil, []string{"application"}, nil); err == nil {
		t.Fatal("expected required component selection to fail")
	}
}

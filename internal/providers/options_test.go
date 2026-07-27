package providers

import (
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func TestApplicationOptionsAndSelection(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{Applications: map[string]blueprint.Application{
		"application": {
			Options: map[string]blueprint.ApplicationOption{
				"smtp": {Description: "SMTP"},
				"imap": {Description: "IMAP"},
			},
		},
	}}}
	options := ApplicationOptions(document)
	if got := []string{options[0].Name, options[1].Name}; !reflect.DeepEqual(got, []string{"imap", "smtp"}) {
		t.Fatalf("options = %#v", got)
	}
	selected, err := SelectApplicationOptions(document, []string{"smtp"}, []string{"imap"}, []string{"smtp"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"imap"}) {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectApplicationOptionsDoesNotTreatRequiredApplicationAsOption(t *testing.T) {
	document := blueprint.Document{Environment: blueprint.Environment{Applications: map[string]blueprint.Application{
		"application": {},
	}}}
	if _, err := SelectApplicationOptions(document, nil, []string{"application"}, nil); err == nil {
		t.Fatal("expected required application selection to fail")
	}
}

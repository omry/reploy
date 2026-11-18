package apt

import (
	"strings"
	"testing"
)

func TestResolveTransactionCompletesRootsOmittedByOlderAPT(t *testing.T) {
	for _, test := range []struct {
		name           string
		roots          []string
		marker, output string
		count          int
	}{
		{"mixed", []string{"ca-certificates", "hello=2.10-2", "mawk"},
			"  MarkInstall ca-certificates:amd64 < none -> 2025 @un puN > FU=1\n  MarkInstall libc6:amd64 < 2.31 @ii pmK > FU=0\n",
			"mawk is already the newest version (1.3).\nInst ca-certificates (2025 Debian:11/stable [all])\nInst hello (2.10-2 Debian:11/stable [amd64])\n", 4},
		{"leaf only", []string{"hello"}, "", "Inst hello (2.10-2 Debian:11/stable [amd64])\n", 1},
		{"retained only", []string{"mawk=1.3"}, "", "mawk is already the newest version (1.3).\n", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker, err := NewResolvePlanMarkerParserV1("amd64", test.roots)
			if err != nil {
				t.Fatal(err)
			}
			transaction, _ := NewResolveInstallParserV1("amd64")
			_, _ = marker.Write([]byte(test.marker))
			for _, chunk := range []string{test.output[:7], test.output[7:]} {
				_, _ = transaction.Write([]byte(chunk))
			}
			packages, err := transaction.Finish()
			if err != nil {
				t.Fatal(err)
			}
			plan, err := marker.FinishWithInstallTransaction(packages)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Packages) != test.count {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}
}

func TestResolveTransactionStillRejectsIncompleteConflictingAndWrongRootEvidence(t *testing.T) {
	for _, test := range []struct{ name, root, marker, output, want string }{
		{"missing", "hello", "  MarkInstall libc6:amd64 < 2.31 @ii pmK > FU=0\n", "", "did not report requested"},
		{"wrong exact install", "hello=2", "", "Inst hello (1 Debian:11/stable [amd64])\n", "does not match"},
		{"wrong exact retained", "hello=2", "", "hello is already the newest version (1).\n", "does not match"},
		{"conflict", "hello", "  MarkInstall hello:amd64 < none -> 2 @un puN > FU=1\n", "Inst hello (1 Debian:11/stable [amd64])\n", "disagree"},
		{"partial marker", "hello", "  MarkInstall hello:amd64 < none -> 1 @un puN > FU=1", "Inst hello (1 Debian:11/stable [amd64])\n", "incomplete line"},
		{"foreign retained", "hello", "", "hello:arm64 is already the newest version (1).\n", "invalid"},
		{"malformed retained", "hello", "", "hello is already the newest version (1)\n", "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker, _ := NewResolvePlanMarkerParserV1("amd64", []string{test.root})
			transaction, _ := NewResolveInstallParserV1("amd64")
			_, _ = marker.Write([]byte(test.marker))
			_, _ = transaction.Write([]byte(test.output))
			packages, err := transaction.Finish()
			if err == nil {
				_, err = marker.FinishWithInstallTransaction(packages)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

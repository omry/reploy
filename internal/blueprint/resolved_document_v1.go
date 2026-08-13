package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/omry/reploy/internal/canonical"
)

const ResolvedDocumentSchemaV1 = "blueprint-resolved-v1"

// ResolvedDocumentV1 is the complete, self-contained resolved blueprint
// persisted in deployment state. Its JSON text is kept as one string because
// canonical-json-v1 deliberately rejects the integer and duration fields in a
// resolved blueprint.
type ResolvedDocumentV1 string

type resolvedDocumentEnvelopeV1 struct {
	Schema   string   `json:"schema"`
	Document Document `json:"document"`
}

func EncodeResolvedDocumentV1(document Document) (ResolvedDocumentV1, error) {
	payload, err := json.Marshal(resolvedDocumentEnvelopeV1{Schema: ResolvedDocumentSchemaV1, Document: document})
	if err != nil {
		return "", fmt.Errorf("encode resolved blueprint: %w", err)
	}
	return ResolvedDocumentV1(payload), nil
}

func DecodeResolvedDocumentV1(payload ResolvedDocumentV1) (Document, error) {
	if payload == "" {
		return Document{}, fmt.Errorf("resolved blueprint is required")
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(payload)))
	decoder.DisallowUnknownFields()
	var envelope resolvedDocumentEnvelopeV1
	if err := decoder.Decode(&envelope); err != nil {
		if environmentID := resolvedDocumentEnvironmentIDV1(payload); environmentID != "" {
			return Document{}, fmt.Errorf("decode resolved blueprint for environment %q: %w", environmentID, err)
		}
		return Document{}, fmt.Errorf("decode resolved blueprint: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Document{}, fmt.Errorf("resolved blueprint contains trailing JSON")
		}
		return Document{}, fmt.Errorf("decode resolved blueprint trailer: %w", err)
	}
	if envelope.Schema != ResolvedDocumentSchemaV1 {
		return Document{}, fmt.Errorf("resolved blueprint schema must be %q", ResolvedDocumentSchemaV1)
	}
	canonicalPayload, err := EncodeResolvedDocumentV1(envelope.Document)
	if err != nil {
		return Document{}, err
	}
	if canonicalPayload != payload {
		return Document{}, fmt.Errorf("resolved blueprint is not in its canonical wire form")
	}
	if err := envelope.Document.Environment.RebuildProviderContributions(); err != nil {
		return Document{}, fmt.Errorf("rebuild resolved blueprint provider contributions: %w", err)
	}
	return envelope.Document, nil
}

func resolvedDocumentEnvironmentIDV1(payload ResolvedDocumentV1) string {
	var probe struct {
		Document struct {
			Environment struct {
				ID string
			}
		}
	}
	if json.Unmarshal([]byte(payload), &probe) != nil {
		return ""
	}
	return probe.Document.Environment.ID
}

func ResolvedDocumentDigestV1(payload ResolvedDocumentV1) (canonical.Digest, error) {
	if _, err := DecodeResolvedDocumentV1(payload); err != nil {
		return "", err
	}
	return canonical.Sum("blueprint-resolved", ResolvedDocumentSchemaV1, string(payload))
}

func DocumentDigestV1(document Document) (canonical.Digest, error) {
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		return "", err
	}
	return ResolvedDocumentDigestV1(payload)
}

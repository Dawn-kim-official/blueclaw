package bluecollar

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"github.com/Dawn-kim-official/blueclaw/toolcontract"
	"os"
	"path/filepath"
	"testing"
)

func TestValidityStateAcceptsReadableNonEmptyArtifact(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "notes.txt")
	writeAgentTestFile(t, artifactPath, "delivered file")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".txt",
		Filename:     "notes.txt",
		RelativePath: "notes.txt",
		path:         artifactPath,
	}})

	if !validityState.Passed {
		t.Fatalf("expected readable non-empty artifact to pass basic validity, got %+v", validityState)
	}
}

func TestValidityStateRejectsPlainTextDOCX(t *testing.T) {
	document := []byte("plain text renamed as docx")
	validityState := buildAttachmentValidityState(t.TempDir(), []toolcontract.FileAttachment{{
		DevicePath:    "/workspace/private/people/person-1/documents/report.docx",
		Filename:      "report.docx",
		SizeBytes:     int64(len(document)),
		ContentBase64: base64.StdEncoding.EncodeToString(document),
	}})

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected plain text docx to fail validity, got %+v", validityState)
	}
	if validityState.InvalidArtifacts[0].Reason != "artifact content does not match .docx format" {
		t.Fatalf("expected docx format mismatch, got %+v", validityState.InvalidArtifacts[0])
	}
}

func TestValidityStateAcceptsMinimalDOCX(t *testing.T) {
	document := minimalDOCX(t)
	validityState := buildAttachmentValidityState(t.TempDir(), []toolcontract.FileAttachment{{
		DevicePath:    "/workspace/private/people/person-1/documents/report.docx",
		Filename:      "report.docx",
		SizeBytes:     int64(len(document)),
		ContentBase64: base64.StdEncoding.EncodeToString(document),
	}})

	if !validityState.Passed {
		t.Fatalf("expected minimal docx to pass validity, got %+v", validityState)
	}
}

func minimalDOCX(t *testing.T) []byte {
	t.Helper()
	document := bytes.Buffer{}
	archive := zip.NewWriter(&document)
	entries := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Report</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range entries {
		writer, errorValue := archive.Create(name)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if _, errorValue := writer.Write([]byte(content)); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if errorValue := archive.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
	return document.Bytes()
}

func TestValidityStateRejectsEmptyArtifact(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactPath := filepath.Join(workspaceRootPath, "deck.html")
	writeAgentTestFile(t, artifactPath, "")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "deck.html",
		RelativePath: "deck.html",
		path:         artifactPath,
	}})

	if validityState.Passed || len(validityState.InvalidArtifacts) != 1 {
		t.Fatalf("expected empty artifact validity failure, got %+v", validityState)
	}
	if validityState.InvalidArtifacts[0].Reason != "artifact file is empty" {
		t.Fatalf("expected empty artifact reason, got %+v", validityState.InvalidArtifacts[0])
	}
}

func TestValidityStateAcceptsDeckWithoutIntentManifest(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, ".blueclaw", "tmp", "hermes-analysis")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	artifactPath := filepath.Join(artifactDirectoryPath, "hermes-analysis.html")
	writeAgentTestFile(t, artifactPath, "<html><body>Hermes Agent pros and cons analysis</body></html>")

	validityState := buildArtifactValidityState([]CompletionArtifact{{
		Suffix:       ".html",
		Filename:     "hermes-analysis.html",
		RelativePath: ".blueclaw/tmp/hermes-analysis/hermes-analysis.html",
		path:         artifactPath,
	}})

	if !validityState.Passed {
		t.Fatalf("expected deck artifact without intent manifest to pass basic validity, got %+v", validityState)
	}
}

func TestAttachmentValidityUsesPayloadBeforePrivatePathStat(t *testing.T) {
	validityState := buildAttachmentValidityState(t.TempDir(), []toolcontract.FileAttachment{{
		DevicePath:    "/workspace/private/people/person-1/artifacts/deck/result.txt",
		Filename:      "result.txt",
		SizeBytes:     6,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("result")),
	}})

	if !validityState.Passed {
		t.Fatalf("expected inline attachment payload to pass without service path access, got %+v", validityState)
	}
}

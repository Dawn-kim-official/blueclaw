package bluecollar

import (
	"archive/zip"
	"os"
	"testing"
)

func writeValidPPTXTestFile(t *testing.T, path string) {
	t.Helper()
	file, errorValue := os.Create(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	zipWriter := zip.NewWriter(file)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "ppt/presentation.xml", "ppt/_rels/presentation.xml.rels", "ppt/slides/slide1.xml", "ppt/slides/slide2.xml"} {
		writer, errorValue := zipWriter.Create(name)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if _, errorValue := writer.Write([]byte("<xml/>")); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if errorValue := zipWriter.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := file.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func writeValidPDFTestFile(t *testing.T, path string) {
	t.Helper()
	writeAgentTestFile(t, path, "%PDF-1.4\n1 0 obj\n<< /Type /Page >>\nendobj\n%%EOF")
}

func writeValidHTMLTestFile(t *testing.T, path string) {
	t.Helper()
	writeAgentTestFile(t, path, "<html><body><main>Deck</main></body></html>")
}

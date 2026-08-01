package bluecollar

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
)

func validateArtifactFormatsPath(path string, formatValues ...string) string {
	suffixes := validatedArtifactFormatSuffixes(formatValues...)
	if len(suffixes) == 0 {
		return ""
	}
	file, errorValue := os.Open(path)
	if errorValue != nil {
		return artifactFormatMismatchReason(suffixes[0])
	}
	defer file.Close()
	fileInformation, errorValue := file.Stat()
	if errorValue != nil {
		return artifactFormatMismatchReason(suffixes[0])
	}
	return validateArtifactFormats(file, fileInformation.Size(), suffixes)
}

func validateArtifactFormatsBytes(content []byte, formatValues ...string) string {
	return validateArtifactFormats(bytes.NewReader(content), int64(len(content)), validatedArtifactFormatSuffixes(formatValues...))
}

func validateArtifactFormats(reader io.ReaderAt, size int64, suffixes []string) string {
	for _, suffix := range suffixes {
		if reason := validateArtifactFormat(reader, size, suffix); reason != "" {
			return reason
		}
	}
	return ""
}

func validateArtifactFormat(reader io.ReaderAt, size int64, suffix string) string {
	if requiredEntries, isOfficeFormat := requiredOfficeFormatEntries(suffix); isOfficeFormat {
		if !zipContainsEntries(reader, size, requiredEntries) {
			return artifactFormatMismatchReason(suffix)
		}
		return ""
	}
	if signature, hasSignature := artifactFormatSignature(suffix); hasSignature && !readerStartsWith(reader, signature) {
		return artifactFormatMismatchReason(suffix)
	}
	return ""
}

func artifactFormatRequiresValidation(suffix string) bool {
	_, isOfficeFormat := requiredOfficeFormatEntries(suffix)
	_, hasSignature := artifactFormatSignature(suffix)
	return isOfficeFormat || hasSignature
}

func validatedArtifactFormatSuffixes(values ...string) []string {
	suffixes := []string{}
	for _, value := range values {
		suffix := artifactValiditySuffix(value)
		if !artifactFormatRequiresValidation(suffix) || stringSliceContains(suffixes, suffix) {
			continue
		}
		suffixes = append(suffixes, suffix)
	}
	return suffixes
}

func requiredOfficeFormatEntries(suffix string) ([]string, bool) {
	switch suffix {
	case ".docx":
		return []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"}, true
	case ".pptx":
		return []string{"[Content_Types].xml", "_rels/.rels", "ppt/presentation.xml"}, true
	case ".xlsx":
		return []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml"}, true
	default:
		return nil, false
	}
}

func artifactFormatSignature(suffix string) ([]byte, bool) {
	switch suffix {
	case ".pdf":
		return []byte("%PDF-"), true
	case ".png":
		return []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, true
	case ".jpg", ".jpeg":
		return []byte{0xff, 0xd8, 0xff}, true
	default:
		return nil, false
	}
}

func zipContainsEntries(reader io.ReaderAt, size int64, requiredEntries []string) bool {
	archive, errorValue := zip.NewReader(reader, size)
	if errorValue != nil {
		return false
	}
	entries := map[string]bool{}
	for _, file := range archive.File {
		entries[file.Name] = true
	}
	for _, requiredEntry := range requiredEntries {
		if !entries[requiredEntry] {
			return false
		}
	}
	return true
}

func readerStartsWith(reader io.ReaderAt, signature []byte) bool {
	content := make([]byte, len(signature))
	bytesRead, _ := reader.ReadAt(content, 0)
	return bytesRead == len(signature) && bytes.Equal(content, signature)
}

func artifactFormatMismatchReason(suffix string) string {
	return "artifact content does not match " + suffix + " format"
}

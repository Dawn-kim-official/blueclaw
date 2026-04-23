package ingestion

type MarkitdownExtractor struct{}

func (markitdownExtractor MarkitdownExtractor) ExtractText(fileName string, content []byte) string {
	_ = fileName
	return string(content)
}

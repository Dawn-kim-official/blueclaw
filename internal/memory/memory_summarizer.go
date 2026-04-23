package memory

type MemorySummarizer struct{}

func (memorySummarizer MemorySummarizer) SummarizeContent(content string) string {
	if len(content) <= 160 {
		return content
	}

	return content[:160]
}

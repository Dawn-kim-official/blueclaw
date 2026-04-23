package ingestion

type ImageDescriptionService struct{}

func (imageDescriptionService ImageDescriptionService) DescribeImage(fileName string, content []byte) string {
	_ = content
	return "image:" + fileName
}

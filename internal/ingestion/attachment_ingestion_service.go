package ingestion

type AttachmentIngestionService struct {
	blobStore               BlobStore
	markitdownExtractor     MarkitdownExtractor
	imageDescriptionService ImageDescriptionService
}

func NewAttachmentIngestionService(blobStore BlobStore) *AttachmentIngestionService {
	return &AttachmentIngestionService{
		blobStore:               blobStore,
		markitdownExtractor:     MarkitdownExtractor{},
		imageDescriptionService: ImageDescriptionService{},
	}
}

func (attachmentIngestionService *AttachmentIngestionService) IngestAttachment(namespace string, fileName string, content []byte) (string, string, error) {
	return attachmentIngestionService.blobStore.PutObject(namespace, fileName, content)
}

func (attachmentIngestionService *AttachmentIngestionService) ExtractAttachmentText(fileName string, content []byte) string {
	return attachmentIngestionService.markitdownExtractor.ExtractText(fileName, content)
}

func (attachmentIngestionService *AttachmentIngestionService) DescribeAttachment(fileName string, content []byte) string {
	return attachmentIngestionService.imageDescriptionService.DescribeImage(fileName, content)
}

package agentcontract

const (
	AgentPartTypeText  = "text"
	AgentPartTypeImage = "image"
	AgentPartTypeFile  = "file"
)

type AgentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Image      *AgentImagePart `json:"image,omitempty"`
	File       *AgentFilePart  `json:"file,omitempty"`
	Source     AgentPartSource `json:"source,omitempty"`
	Visibility string          `json:"visibility,omitempty"`
}

type AgentImagePart struct {
	MimeType   string `json:"mimeType,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
	Path       string `json:"path,omitempty"`
	Filename   string `json:"filename,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type AgentFilePart struct {
	Path              string `json:"path,omitempty"`
	Filename          string `json:"filename,omitempty"`
	ContentType       string `json:"contentType,omitempty"`
	SizeBytes         int64  `json:"sizeBytes,omitempty"`
	MarkdownPreview   string `json:"markdownPreview,omitempty"`
	ConversionStatus  string `json:"conversionStatus,omitempty"`
	ConversionMessage string `json:"conversionMessage,omitempty"`
}

type AgentPartSource struct {
	Platform      string `json:"platform,omitempty"`
	MessageID     string `json:"messageID,omitempty"`
	FileID        string `json:"fileID,omitempty"`
	ObservationID string `json:"observationID,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
}

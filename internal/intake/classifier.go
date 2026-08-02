package intake

import (
	"time"

	"github.com/Dawn-kim-official/blueclaw/model"
)

type Classifier struct {
	languageModel model.LanguageModelProvider
}

func NewClassifier(languageModel model.LanguageModelProvider) *Classifier {
	return &Classifier{languageModel: languageModel}
}

func classificationLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue != nil {
		return time.Local
	}
	return location
}

func formatContextTimestamp(sentAt time.Time) string {
	if sentAt.IsZero() {
		return ""
	}
	return sentAt.In(classificationLocation()).Format("01-02 15:04")
}

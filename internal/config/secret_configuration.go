package config

import (
	"encoding/json"
	"os"
)

type SecretConfiguration struct {
	Version     int                      `json:"version"`
	Ciphertext  string                   `json:"ciphertext"`
	KeyEnvelope KeyEnvelopeConfiguration `json:"keyEnvelope"`
}

type KeyEnvelopeConfiguration struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
}

func LoadSecretConfiguration(path string) (SecretConfiguration, error) {
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return SecretConfiguration{}, errorValue
	}

	var configuration SecretConfiguration
	errorValue = json.Unmarshal(document, &configuration)
	if errorValue != nil {
		return SecretConfiguration{}, errorValue
	}

	return configuration, nil
}

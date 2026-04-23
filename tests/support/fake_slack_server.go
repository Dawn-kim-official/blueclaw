package support

func SlackMessagePayload() []byte {
	return []byte(`{"type":"message","user":"U123","channel":"C123","text":"hello"}`)
}

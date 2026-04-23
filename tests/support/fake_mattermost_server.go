package support

func MattermostMessagePayload() []byte {
	return []byte(`{"event":"posted","user_id":"user-1","channel_id":"channel-1","message":"hello"}`)
}

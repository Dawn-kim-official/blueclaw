package mattermost

type Connector struct {
	eventParser EventParser
}

func NewConnector() *Connector {
	return &Connector{eventParser: EventParser{}}
}

func (connector *Connector) StartListening() error {
	return nil
}

func (connector *Connector) SendDirectReply(conversationID string, message string) string {
	return conversationID + ":" + message
}

func (connector *Connector) SendChannelReply(conversationID string, message string) string {
	return conversationID + ":" + message
}

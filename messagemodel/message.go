package messagemodel

import (
	"encoding/json"
)

type MessageAttributes map[string]Attribute

type Attribute struct {
	Type  string
	Value string
}

type MessageMetadata struct {
	MessageId         string
	ReceiptHandle     string
	MessageAttributes map[string]string
}

type SNSMessageBody struct {
	MessageAttributes MessageAttributes
	Message           string
}

type Message struct {
	Content  string
	Metadata MessageMetadata
}

func New(content string, metadata MessageMetadata) *Message {
	return &Message{
		Content:  content,
		Metadata: metadata,
	}
}

func (m *Message) Unmarshal(v interface{}) error {
	err := json.Unmarshal([]byte(m.Content), v)

	if err != nil {
		return err
	}

	return nil
}

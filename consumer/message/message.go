package message

import (
	"encoding/json"
	"github.com/aws/aws-sdk-go/service/sqs"
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

const (
	SQS = "SQS"
	SNS = "SNS"
)

func New(sqsMessage *sqs.Message) *Message {
	content := getContent(sqsMessage)
	metadata := MessageMetadata{
		MessageId:         *sqsMessage.MessageId,
		ReceiptHandle:     *sqsMessage.ReceiptHandle,
		MessageAttributes: getMessageAttributes(sqsMessage),
	}

	return &Message{
		Content:  content,
		Metadata: metadata,
	}
}

func getMessageSource(sqsMessage *sqs.Message) string {
	snsBody := SNSMessageBody{}

	err := json.Unmarshal([]byte(*sqsMessage.Body), &snsBody)

	if err != nil {
		return SQS
	}

	if snsBody.Message != "" {
		return SNS
	}

	return SQS
}

func getContent(sqsMessage *sqs.Message) string {
	messageSource := getMessageSource(sqsMessage)

	if messageSource == SNS {
		snsBody := SNSMessageBody{}

		json.Unmarshal([]byte(*sqsMessage.Body), &snsBody)

		return snsBody.Message
	}

	return *sqsMessage.Body
}

func getMessageAttributes(message *sqs.Message) map[string]string {
	attributes := make(map[string]string)
	messageSource := getMessageSource(message)

	for key, value := range message.Attributes {
		attributes[key] = *value
	}

	if messageSource == SQS {
		for key, value := range message.MessageAttributes {
			attributes[key] = *value.StringValue
		}

		return attributes
	}

	var messageBody SNSMessageBody

	json.Unmarshal([]byte(*message.Body), &messageBody)

	for key, attribute := range messageBody.MessageAttributes {
		attributes[key] = attribute.Value
	}

	return attributes
}

func (m *Message) Unmarshal(v interface{}) error {
	err := json.Unmarshal([]byte(m.Content), v)

	if err != nil {
		return err
	}

	return nil
}

package gosqs

import (
	"encoding/base64"
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

func NewMessage(sqsMessage *sqs.Message) *Message {
	content := getContent(sqsMessage)
	var messageID *string
	var receiptHandle *string

	if sqsMessage != nil {
		messageID = sqsMessage.MessageId
		receiptHandle = sqsMessage.ReceiptHandle
	}

	metadata := MessageMetadata{
		MessageId:         getStringValue(messageID),
		ReceiptHandle:     getStringValue(receiptHandle),
		MessageAttributes: getMessageAttributes(sqsMessage),
	}

	return &Message{
		Content:  content,
		Metadata: metadata,
	}
}

func getMessageSource(sqsMessage *sqs.Message) string {
	if sqsMessage == nil || sqsMessage.Body == nil {
		return SQS
	}

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
	if sqsMessage == nil || sqsMessage.Body == nil {
		return ""
	}

	messageSource := getMessageSource(sqsMessage)

	if messageSource == SNS {
		snsBody := SNSMessageBody{}

		json.Unmarshal([]byte(*sqsMessage.Body), &snsBody)

		return snsBody.Message
	}

	return *sqsMessage.Body
}

func getStringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func getAttributeValue(value *sqs.MessageAttributeValue) string {
	if value == nil {
		return ""
	}

	if value.StringValue != nil {
		return *value.StringValue
	}

	if len(value.BinaryValue) > 0 {
		return base64.StdEncoding.EncodeToString(value.BinaryValue)
	}

	return ""
}

func getMessageAttributes(message *sqs.Message) map[string]string {
	attributes := make(map[string]string)
	if message == nil {
		return attributes
	}

	messageSource := getMessageSource(message)

	for key, value := range message.Attributes {
		if value != nil {
			attributes[key] = *value
		}
	}

	if messageSource == SQS {
		for key, value := range message.MessageAttributes {
			attributes[key] = getAttributeValue(value)
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

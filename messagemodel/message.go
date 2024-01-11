package messagemodel

import (
	"encoding/json"
	"fmt"

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

	if messageSource == SQS {
		for key, value := range message.MessageAttributes {
			attributes[key] = *value.StringValue
		}

		return attributes
	}

	var messageBody map[string]interface{}

	err := json.Unmarshal([]byte(*message.Body), &messageBody)

	if err != nil {
		fmt.Println(err.Error())

		return attributes
	}

	messageAttributes := messageBody["MessageAttributes"].(map[string]interface{})

	for key, value := range messageAttributes {
		attribute := value.(map[string]interface{})

		attributes[key] = attribute["Value"].(string)
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

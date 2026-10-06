package gosqs

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// MessageFormat selects payload interpretation. The zero value preserves SQS bodies.
type MessageFormat int

const (
	MessageFormatSQS MessageFormat = iota
	MessageFormatSNS
)

// Attribute preserves the SDK data type and its string or binary value.
type Attribute struct {
	DataType    string
	StringValue *string
	BinaryValue []byte
}

type MessageMetadata struct {
	MessageID         string
	ReceiptHandle     string
	QueueURL          string
	SystemAttributes  map[string]string
	MessageAttributes map[string]Attribute
}

type Message struct {
	Content  string
	Metadata MessageMetadata
}

// NewMessage copies a raw SQS message without automatically unwrapping SNS.
func NewMessage(raw *types.Message) *Message {
	m := &Message{Metadata: MessageMetadata{SystemAttributes: map[string]string{}, MessageAttributes: map[string]Attribute{}}}
	if raw == nil {
		return m
	}
	m.Content = aws.ToString(raw.Body)
	m.Metadata.MessageID = aws.ToString(raw.MessageId)
	m.Metadata.ReceiptHandle = aws.ToString(raw.ReceiptHandle)
	for k, v := range raw.Attributes {
		m.Metadata.SystemAttributes[k] = v
	}
	for k, v := range raw.MessageAttributes {
		attr := Attribute{DataType: aws.ToString(v.DataType), BinaryValue: append([]byte(nil), v.BinaryValue...)}
		if v.StringValue != nil {
			attr.StringValue = aws.String(*v.StringValue)
		}
		m.Metadata.MessageAttributes[k] = attr
	}
	return m
}

func unwrapSNS(m *Message) error {
	var envelope struct {
		Type              string
		TopicArn          string
		MessageId         string
		Message           *string
		MessageAttributes map[string]struct {
			Type  string
			Value string
		}
	}
	if err := json.Unmarshal([]byte(m.Content), &envelope); err != nil {
		return fmt.Errorf("invalid SNS envelope: %w", err)
	}
	if envelope.Type != "Notification" || envelope.TopicArn == "" || envelope.MessageId == "" || envelope.Message == nil {
		return errors.New("SNS notification requires Type, TopicArn, MessageId, and Message")
	}
	attributes := make(map[string]Attribute, len(envelope.MessageAttributes))
	for k, v := range envelope.MessageAttributes {
		attr := Attribute{DataType: v.Type}
		switch strings.Split(v.Type, ".")[0] {
		case "Binary":
			decoded, err := base64.StdEncoding.DecodeString(v.Value)
			if err != nil {
				return fmt.Errorf("invalid binary SNS attribute %q: %w", k, err)
			}
			attr.BinaryValue = decoded
		case "String", "String.Array", "Number":
			attr.StringValue = aws.String(v.Value)
		default:
			return fmt.Errorf("invalid SNS attribute type %q", v.Type)
		}
		attributes[k] = attr
	}
	m.Content = *envelope.Message
	// Preserve SQS custom attributes, with SNS payload attributes taking precedence.
	for k, v := range attributes {
		m.Metadata.MessageAttributes[k] = v
	}
	return nil
}

// Unmarshal decodes the effective message content into v.
func (m *Message) Unmarshal(v any) error { return json.Unmarshal([]byte(m.Content), v) }

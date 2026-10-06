package gosqs_test

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	gosqs "github.com/inaciogu/go-sqs/v2"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNewMessagePreservesSQSAndCopiesAttributes(t *testing.T) {
	raw := message(`{"Message":"text","orderId":123}`)
	raw.MessageAttributes = map[string]types.MessageAttributeValue{
		"text":                    {DataType: aws.String("String.custom"), StringValue: aws.String("value")},
		"binary":                  {DataType: aws.String("Binary"), BinaryValue: []byte("hi")},
		"ApproximateReceiveCount": {DataType: aws.String("Number"), StringValue: aws.String("99")},
	}
	m := gosqs.NewMessage(&raw)
	require.Equal(t, *raw.Body, m.Content)
	require.Equal(t, "id", m.Metadata.MessageID)
	require.Equal(t, "3", m.Metadata.SystemAttributes["ApproximateReceiveCount"])
	require.Equal(t, "99", *m.Metadata.MessageAttributes["ApproximateReceiveCount"].StringValue)
	require.Equal(t, "String.custom", m.Metadata.MessageAttributes["text"].DataType)
	raw.MessageAttributes["binary"].BinaryValue[0] = 'x'
	*raw.MessageAttributes["text"].StringValue = "changed"
	raw.Attributes["ApproximateReceiveCount"] = "changed"
	require.Equal(t, []byte("hi"), m.Metadata.MessageAttributes["binary"].BinaryValue)
	require.Equal(t, "value", *m.Metadata.MessageAttributes["text"].StringValue)
	require.Equal(t, "3", m.Metadata.SystemAttributes["ApproximateReceiveCount"])
	var body struct {
		OrderID int `json:"orderId"`
	}
	require.NoError(t, m.Unmarshal(&body))
	require.Equal(t, 123, body.OrderID)
	require.Empty(t, gosqs.NewMessage(nil).Content)
	require.Error(t, (&gosqs.Message{Content: "invalid"}).Unmarshal(&body))
}

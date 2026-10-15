package gosqs_test

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	gosqs "github.com/inaciogu/go-sqs/v2"
	"github.com/stretchr/testify/require"
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

func TestNewMessageInitializesEmptyAttributeMaps(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  *types.Message
	}{
		{name: "nil message"},
		{name: "nil maps", raw: &types.Message{}},
		{name: "empty maps", raw: &types.Message{
			Attributes: map[string]string{}, MessageAttributes: map[string]types.MessageAttributeValue{},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := gosqs.NewMessage(tc.raw)
			require.NotNil(t, m.Metadata.SystemAttributes)
			require.NotNil(t, m.Metadata.MessageAttributes)
			require.Empty(t, m.Metadata.SystemAttributes)
			require.Empty(t, m.Metadata.MessageAttributes)
			m.Metadata.SystemAttributes["new"] = "system"
			m.Metadata.MessageAttributes["new"] = gosqs.Attribute{DataType: "String", StringValue: aws.String("custom")}
			if tc.raw != nil {
				require.Empty(t, tc.raw.Attributes)
				require.Empty(t, tc.raw.MessageAttributes)
			}
		})
	}
}

func TestNewMessageSystemAttributesAreIndependent(t *testing.T) {
	raw := &types.Message{Attributes: map[string]string{"ApproximateReceiveCount": "3", "SentTimestamp": "123"}}
	m := gosqs.NewMessage(raw)
	require.Equal(t, raw.Attributes, m.Metadata.SystemAttributes)
	m.Metadata.SystemAttributes["ApproximateReceiveCount"] = "changed"
	delete(m.Metadata.SystemAttributes, "SentTimestamp")
	m.Metadata.SystemAttributes["new"] = "value"
	require.Equal(t, map[string]string{"ApproximateReceiveCount": "3", "SentTimestamp": "123"}, raw.Attributes)
}

// Package mocks provides test doubles for the consumer API.

package mocks

import (
	"context"

	sqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	mock "github.com/stretchr/testify/mock"
)

// QueueClient implements the SDK v2-compatible client interface for tests.
type QueueClient struct {
	mock.Mock
}

// ChangeMessageVisibility provides a mock function with given fields: input
func (_m *QueueClient) ChangeMessageVisibility(ctx context.Context, input *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	ret := _m.Called(ctx, input, optFns)

	var r0 *sqs.ChangeMessageVisibilityOutput
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)); ok {
		return rf(ctx, input, optFns...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) *sqs.ChangeMessageVisibilityOutput); ok {
		r0 = rf(ctx, input, optFns...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*sqs.ChangeMessageVisibilityOutput)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) error); ok {
		r1 = rf(ctx, input, optFns...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// DeleteMessage provides a mock function with given fields: input
func (_m *QueueClient) DeleteMessage(ctx context.Context, input *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	ret := _m.Called(ctx, input, optFns)

	var r0 *sqs.DeleteMessageOutput
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)); ok {
		return rf(ctx, input, optFns...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) *sqs.DeleteMessageOutput); ok {
		r0 = rf(ctx, input, optFns...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*sqs.DeleteMessageOutput)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) error); ok {
		r1 = rf(ctx, input, optFns...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetQueueUrl provides a mock function with given fields: input
func (_m *QueueClient) GetQueueUrl(ctx context.Context, input *sqs.GetQueueUrlInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	ret := _m.Called(ctx, input, optFns)

	var r0 *sqs.GetQueueUrlOutput
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)); ok {
		return rf(ctx, input, optFns...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) *sqs.GetQueueUrlOutput); ok {
		r0 = rf(ctx, input, optFns...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*sqs.GetQueueUrlOutput)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) error); ok {
		r1 = rf(ctx, input, optFns...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ListQueues provides a mock function with given fields: input
func (_m *QueueClient) ListQueues(ctx context.Context, input *sqs.ListQueuesInput, optFns ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	ret := _m.Called(ctx, input, optFns)

	var r0 *sqs.ListQueuesOutput
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.ListQueuesInput, ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)); ok {
		return rf(ctx, input, optFns...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.ListQueuesInput, ...func(*sqs.Options)) *sqs.ListQueuesOutput); ok {
		r0 = rf(ctx, input, optFns...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*sqs.ListQueuesOutput)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *sqs.ListQueuesInput, ...func(*sqs.Options)) error); ok {
		r1 = rf(ctx, input, optFns...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ReceiveMessage provides a mock function with given fields: input
func (_m *QueueClient) ReceiveMessage(ctx context.Context, input *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	ret := _m.Called(ctx, input, optFns)

	var r0 *sqs.ReceiveMessageOutput
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)); ok {
		return rf(ctx, input, optFns...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) *sqs.ReceiveMessageOutput); ok {
		r0 = rf(ctx, input, optFns...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*sqs.ReceiveMessageOutput)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) error); ok {
		r1 = rf(ctx, input, optFns...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// NewQueueClient creates a new instance of QueueClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewQueueClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *QueueClient {
	mock := &QueueClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}

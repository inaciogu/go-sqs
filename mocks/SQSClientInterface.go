// Code generated by mockery v2.33.2. DO NOT EDIT.

package mocks

import (
	sqs "github.com/aws/aws-sdk-go/service/sqs"
	mock "github.com/stretchr/testify/mock"
)

// SQSClientInterface is an autogenerated mock type for the SQSClientInterface type
type SQSClientInterface struct {
	mock.Mock
}

// GetQueueUrl provides a mock function with given fields:
func (_m *SQSClientInterface) GetQueueUrl() *string {
	ret := _m.Called()

	var r0 *string
	if rf, ok := ret.Get(0).(func() *string); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*string)
		}
	}

	return r0
}

// GetQueues provides a mock function with given fields: prefix
func (_m *SQSClientInterface) GetQueues(prefix string) []*string {
	ret := _m.Called(prefix)

	var r0 []*string
	if rf, ok := ret.Get(0).(func(string) []*string); ok {
		r0 = rf(prefix)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*string)
		}
	}

	return r0
}

// Poll provides a mock function with given fields:
func (_m *SQSClientInterface) Poll() {
	_m.Called()
}

// ProcessMessage provides a mock function with given fields: message
func (_m *SQSClientInterface) ProcessMessage(message *sqs.Message) {
	_m.Called(message)
}

// ReceiveMessages provides a mock function with given fields: queueUrl
func (_m *SQSClientInterface) ReceiveMessages(queueUrl string) error {
	ret := _m.Called(queueUrl)

	var r0 error
	if rf, ok := ret.Get(0).(func(string) error); ok {
		r0 = rf(queueUrl)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewSQSClientInterface creates a new instance of SQSClientInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewSQSClientInterface(t interface {
	mock.TestingT
	Cleanup(func())
}) *SQSClientInterface {
	mock := &SQSClientInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
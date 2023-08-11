// Code generated by mockery v2.14.0. DO NOT EDIT.

package mocks

import (
	msgsender "github.com/dydxprotocol/v4/indexer/msgsender"
	mock "github.com/stretchr/testify/mock"
)

// IndexerMessageSender is an autogenerated mock type for the IndexerMessageSender type
type IndexerMessageSender struct {
	mock.Mock
}

// Close provides a mock function with given fields:
func (_m *IndexerMessageSender) Close() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Enabled provides a mock function with given fields:
func (_m *IndexerMessageSender) Enabled() bool {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// SendOffchainData provides a mock function with given fields: message
func (_m *IndexerMessageSender) SendOffchainData(message msgsender.Message) {
	_m.Called(message)
}

// SendOnchainData provides a mock function with given fields: message
func (_m *IndexerMessageSender) SendOnchainData(message msgsender.Message) {
	_m.Called(message)
}

type mockConstructorTestingTNewIndexerMessageSender interface {
	mock.TestingT
	Cleanup(func())
}

// NewIndexerMessageSender creates a new instance of IndexerMessageSender. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewIndexerMessageSender(t mockConstructorTestingTNewIndexerMessageSender) *IndexerMessageSender {
	mock := &IndexerMessageSender{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
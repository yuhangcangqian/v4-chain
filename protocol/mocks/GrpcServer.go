// Code generated by mockery v2.14.0. DO NOT EDIT.

package mocks

import (
	grpc "google.golang.org/grpc"

	mock "github.com/stretchr/testify/mock"

	net "net"
)

// GrpcServer is an autogenerated mock type for the GrpcServer type
type GrpcServer struct {
	mock.Mock
}

// RegisterService provides a mock function with given fields: sd, ss
func (_m *GrpcServer) RegisterService(sd *grpc.ServiceDesc, ss interface{}) {
	_m.Called(sd, ss)
}

// Serve provides a mock function with given fields: lis
func (_m *GrpcServer) Serve(lis net.Listener) error {
	ret := _m.Called(lis)

	var r0 error
	if rf, ok := ret.Get(0).(func(net.Listener) error); ok {
		r0 = rf(lis)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Stop provides a mock function with given fields:
func (_m *GrpcServer) Stop() {
	_m.Called()
}

type mockConstructorTestingTNewGrpcServer interface {
	mock.TestingT
	Cleanup(func())
}

// NewGrpcServer creates a new instance of GrpcServer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewGrpcServer(t mockConstructorTestingTNewGrpcServer) *GrpcServer {
	mock := &GrpcServer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
package hotload

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

var _ = Describe("PrometheusMetric", func() {
	It("Should register a prometheus metric", func() {
		// This test is a placeholder for a real test
		err := prometheus.Register(sqlStmtsSummary)
		Expect(err).Should(HaveOccurred())
		Expect(errors.As(err, &prometheus.AlreadyRegisteredError{})).Should(BeTrue())
	})
})

var _ = Describe("PromUnaryServerInterceptor", func() {
	It("Should return a unary server interceptor", func() {
		validationHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
			labels := GetExecLabelsFromContext(ctx)

			Expect(labels).ShouldNot(BeNil())
			Expect(labels[GRPCMethodKey]).Should(Equal("List"))
			Expect(labels[GRPCServiceKey]).Should(Equal("infoblox.service.SampleService"))

			return nil, nil
		}

		promUnaryServerInterceptor := PromUnaryServerInterceptor()
		promUnaryServerInterceptor(context.Background(), struct{}{}, &grpc.UnaryServerInfo{
			FullMethod: "/infoblox.service.SampleService/List",
		}, validationHandler)
	})
})

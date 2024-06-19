package hotload

import (
	"errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

var _ = Describe("PrometheusMetric", func() {
	It("Should register a prometheus metric", func() {
		// This test is a placeholder for a real test
		err := prometheus.Register(sqlStmtsSummary)
		Expect(err).Should(HaveOccurred())
		Expect(errors.As(err, &prometheus.AlreadyRegisteredError{})).Should(BeTrue())
	})
})

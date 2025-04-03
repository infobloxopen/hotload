package integrationtests

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIntegrationtests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Tests")
}

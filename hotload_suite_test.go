package hotload_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestHotload(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hotload Suite")
}

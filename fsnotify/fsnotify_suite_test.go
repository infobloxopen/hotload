package fsnotify

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestFsnotify(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fsnotify Suite")
}

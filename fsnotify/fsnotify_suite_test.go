package fsnotify

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFsnotify(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fsnotify Suite")
}

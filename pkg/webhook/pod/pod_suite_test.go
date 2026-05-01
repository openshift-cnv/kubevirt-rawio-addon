package pod

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPodWebhook(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pod Webhook Suite")
}

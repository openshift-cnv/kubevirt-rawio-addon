package vmi

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVMIWebhook(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VMI Webhook Suite")
}

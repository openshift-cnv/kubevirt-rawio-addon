package vmi

import (
	"context"
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func makeVMIRequest(annotations map[string]string) admission.Request {
	return makeVMIRequestWithStatus(annotations, nil)
}

func makeVMIRequestWithStatus(annotations map[string]string, status map[string]any) admission.Request {
	vmi := map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachineInstance",
		"metadata": map[string]any{
			"name":        "test-vmi",
			"namespace":   "default",
			"annotations": annotations,
		},
		"spec": map[string]any{},
	}
	if status != nil {
		vmi["status"] = status
	}
	raw, _ := json.Marshal(vmi)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       "test-uid",
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: raw,
			},
			Resource: metav1.GroupVersionResource{
				Group:    "kubevirt.io",
				Version:  "v1",
				Resource: "virtualmachineinstances",
			},
		},
	}
}

func findPatchValue(resp admission.Response, path string) any {
	for _, p := range resp.Patches {
		if p.Path == path {
			return p.Value
		}
	}
	return nil
}

var _ = Describe("VMI Mutating Webhook", func() {
	var handler *MutatingHandler

	BeforeEach(func() {
		handler = NewMutatingHandler()
	})

	Context("when VMI has rawio annotation", func() {
		BeforeEach(func() {
			Expect(os.Setenv(envSidecarImage, "quay.io/test/rawio-sidecar:latest")).To(Succeed())
			DeferCleanup(os.Unsetenv, envSidecarImage)
		})

		It("should inject hookSidecars annotation", func() {
			req := makeVMIRequest(map[string]string{
				annotationRawIO: "sda,sdb",
			})

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).NotTo(BeEmpty())

			sidecarsRaw, ok := findPatchValue(resp, "/metadata/annotations/hooks.kubevirt.io~1hookSidecars").(string)
			Expect(ok).To(BeTrue())

			var sidecars []HookSidecar
			Expect(json.Unmarshal([]byte(sidecarsRaw), &sidecars)).To(Succeed())
			Expect(sidecars).To(HaveLen(1))
			Expect(sidecars[0].Image).To(Equal("quay.io/test/rawio-sidecar:latest"))

			// RuntimeUser is set via /status (add) or /status/runtimeUser (replace)
			runtimeUser := findPatchValue(resp, "/status/runtimeUser")
			if runtimeUser == nil {
				status, ok := findPatchValue(resp, "/status").(map[string]any)
				Expect(ok).To(BeTrue())
				runtimeUser = status["runtimeUser"]
			}
			Expect(runtimeUser).To(BeEquivalentTo(0))
		})

		It("should append to existing sidecars", func() {
			req := makeVMIRequest(map[string]string{
				annotationRawIO:        "sda",
				annotationHookSidecars: `[{"image":"quay.io/other/sidecar:v1","imagePullPolicy":"IfNotPresent"}]`,
			})

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).NotTo(BeEmpty())

			sidecarsRaw, ok := findPatchValue(resp, "/metadata/annotations/hooks.kubevirt.io~1hookSidecars").(string)
			Expect(ok).To(BeTrue())

			var sidecars []HookSidecar
			Expect(json.Unmarshal([]byte(sidecarsRaw), &sidecars)).To(Succeed())
			Expect(sidecars).To(HaveLen(2))
			Expect(sidecars[0].Image).To(Equal("quay.io/other/sidecar:v1"))
			Expect(sidecars[1].Image).To(Equal("quay.io/test/rawio-sidecar:latest"))
		})

		It("should not add duplicate sidecar but still set runtimeUser", func() {
			req := makeVMIRequest(map[string]string{
				annotationRawIO:        "sda",
				annotationHookSidecars: `[{"image":"quay.io/test/rawio-sidecar:latest","imagePullPolicy":"IfNotPresent"}]`,
			})

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())

			// No sidecar patch, but runtimeUser should still be set
			Expect(findPatchValue(resp, "/metadata/annotations/hooks.kubevirt.io~1hookSidecars")).To(BeNil())

			status, ok := findPatchValue(resp, "/status").(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(status["runtimeUser"]).To(BeEquivalentTo(0))
		})

		It("should make no changes when sidecar present and runtimeUser already 0", func() {
			req := makeVMIRequestWithStatus(
				map[string]string{
					annotationRawIO:        "sda",
					annotationHookSidecars: `[{"image":"quay.io/test/rawio-sidecar:latest","imagePullPolicy":"IfNotPresent"}]`,
				},
				map[string]any{"runtimeUser": float64(0)},
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).To(BeEmpty())
		})
	})

	Context("when VMI does not have rawio annotation", func() {
		It("should allow without patching", func() {
			req := makeVMIRequest(map[string]string{
				"some-other-annotation": "value",
			})

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).To(BeEmpty())
		})
	})
})

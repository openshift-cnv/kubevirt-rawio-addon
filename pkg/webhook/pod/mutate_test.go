package pod

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

func makePodRequest(annotations map[string]string, containers []corev1.Container) admission.Request {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "virt-launcher-test-xyz",
			Namespace:   "default",
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: containers,
		},
	}
	raw, _ := json.Marshal(pod)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       "test-uid",
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: raw,
			},
			Resource: metav1.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "pods",
			},
		},
	}
}

func applyPatches(req admission.Request, resp admission.Response) corev1.Pod {
	patchBytes, err := json.Marshal(resp.Patches)
	Expect(err).NotTo(HaveOccurred())

	patch, err := jsonpatch.DecodePatch(patchBytes)
	Expect(err).NotTo(HaveOccurred())

	modified, err := patch.Apply(req.Object.Raw)
	Expect(err).NotTo(HaveOccurred())

	var result corev1.Pod
	Expect(json.Unmarshal(modified, &result)).To(Succeed())
	return result
}

var _ = Describe("Pod Mutating Webhook", func() {
	var handler *MutatingHandler

	BeforeEach(func() {
		handler = NewMutatingHandler()
	})

	Context("when pod has rawio annotation", func() {
		It("should add SYS_RAWIO and SETFCAP capabilities and setcap command", func() {
			req := makePodRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{
					{
						Name:    "compute",
						Image:   "registry/virt-launcher:latest",
						Command: []string{"/usr/bin/virt-launcher-monitor"},
						Args:    []string{"--some-flag", "value"},
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_BIND_SERVICE", "SYS_NICE"},
							},
						},
					},
				},
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).NotTo(BeEmpty())

			pod := applyPatches(req, resp)
			compute := findContainer(pod.Spec.Containers, "compute")
			Expect(compute).NotTo(BeNil())

			Expect(compute.SecurityContext.Capabilities.Add).To(ContainElements(
				corev1.Capability("NET_BIND_SERVICE"),
				corev1.Capability("SYS_NICE"),
				corev1.Capability("SYS_RAWIO"),
				corev1.Capability("SETFCAP"),
			))

			Expect(compute.Command).To(HaveLen(3))
			Expect(compute.Command[0]).To(Equal("/bin/bash"))
			Expect(compute.Command[1]).To(Equal("-c"))
			Expect(compute.Command[2]).To(ContainSubstring("setcap cap_net_bind_service,cap_sys_rawio+ep /usr/libexec/qemu-kvm"))
			Expect(compute.Args).To(BeNil())
		})

		It("should not duplicate capabilities if already present", func() {
			req := makePodRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{
					{
						Name:    "compute",
						Image:   "registry/virt-launcher:latest",
						Command: []string{"/usr/bin/virt-launcher-monitor"},
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_BIND_SERVICE", "SYS_RAWIO", "SETFCAP"},
							},
						},
					},
				},
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())

			pod := applyPatches(req, resp)
			compute := findContainer(pod.Spec.Containers, "compute")
			Expect(compute).NotTo(BeNil())

			rawioCount := 0
			for _, c := range compute.SecurityContext.Capabilities.Add {
				if c == "SYS_RAWIO" {
					rawioCount++
				}
			}
			Expect(rawioCount).To(Equal(1))
		})

		It("should allow when no compute container found", func() {
			req := makePodRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{
					{Name: "sidecar", Image: "registry/sidecar:latest"},
				},
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
		})
	})

	Context("when pod does not have rawio annotation", func() {
		It("should allow without patching", func() {
			req := makePodRequest(
				map[string]string{},
				[]corev1.Container{
					{Name: "compute", Image: "registry/virt-launcher:latest"},
				},
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).To(BeEmpty())
		})
	})
})

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

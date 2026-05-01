package pod

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	securityv1 "github.com/openshift/api/security/v1"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type mockSCCReviewer struct {
	allowedBy *corev1.ObjectReference
	err       error
	lastReview *securityv1.PodSecurityPolicySubjectReview
}

func (m *mockSCCReviewer) Review(_ context.Context, _ string, review *securityv1.PodSecurityPolicySubjectReview) (*securityv1.PodSecurityPolicySubjectReview, error) {
	m.lastReview = review
	if m.err != nil {
		return nil, m.err
	}
	result := review.DeepCopy()
	result.Status.AllowedBy = m.allowedBy
	return result, nil
}

func makeValidatingRequest(
	annotations map[string]string,
	containers []corev1.Container,
	username string,
	serviceAccountName string,
	secCtx *corev1.PodSecurityContext,
	operation admissionv1.Operation,
) admission.Request {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "virt-launcher-test-xyz",
			Namespace:   "test-ns",
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers:         containers,
			ServiceAccountName: serviceAccountName,
			SecurityContext:    secCtx,
		},
	}
	raw, _ := json.Marshal(pod)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       "test-uid",
			Namespace: "test-ns",
			Operation: operation,
			Object: runtime.RawExtension{
				Raw: raw,
			},
			Resource: metav1.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "pods",
			},
			UserInfo: authenticationv1.UserInfo{
				Username: username,
			},
		},
	}
}

var _ = Describe("Pod Validating Webhook", func() {
	var (
		handler  *ValidatingHandler
		reviewer *mockSCCReviewer
	)

	virtControllerSA := "system:serviceaccount:openshift-cnv:kubevirt-controller"

	computeContainer := func() corev1.Container {
		return corev1.Container{
			Name:  "compute",
			Image: "registry/virt-launcher:latest",
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{"NET_BIND_SERVICE", "SYS_NICE", "SYS_RAWIO", "SETFCAP"},
				},
			},
		}
	}

	BeforeEach(func() {
		reviewer = &mockSCCReviewer{}
		handler = &ValidatingHandler{
			reviewer:         reviewer,
			virtControllerSA: virtControllerSA,
		}
	})

	Context("when pod has rawio annotation and is created by virt-controller", func() {
		It("should allow when SCC review passes", func() {
			reviewer.allowedBy = &corev1.ObjectReference{Name: "rawio-virt-controller"}

			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{computeContainer()},
				virtControllerSA,
				"default",
				nil,
				admissionv1.Create,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Result.Message).To(ContainSubstring("rawio-virt-controller"))
		})

		It("should deny when SCC review fails", func() {
			reviewer.allowedBy = nil

			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{computeContainer()},
				virtControllerSA,
				"default",
				nil,
				admissionv1.Create,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(ContainSubstring("does not have an SCC"))
		})

		It("should return error when SCC review API fails", func() {
			reviewer.err = fmt.Errorf("connection refused")

			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{computeContainer()},
				virtControllerSA,
				"default",
				nil,
				admissionv1.Create,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Code).To(Equal(int32(500)))
		})

		It("should build synthetic spec with only compute container and pod security context", func() {
			reviewer.allowedBy = &corev1.ObjectReference{Name: "rawio-virt-controller"}
			podSecCtx := &corev1.PodSecurityContext{
				RunAsUser: ptrInt64(0),
			}

			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{
					computeContainer(),
					{Name: "sidecar", Image: "registry/istio:latest"},
				},
				virtControllerSA,
				"my-sa",
				podSecCtx,
				admissionv1.Create,
			)

			handler.Handle(context.Background(), req)
			Expect(reviewer.lastReview).NotTo(BeNil())

			spec := reviewer.lastReview.Spec
			Expect(spec.User).To(Equal("system:serviceaccount:test-ns:my-sa"))
			Expect(spec.Groups).To(ConsistOf(
				"system:serviceaccounts",
				"system:serviceaccounts:test-ns",
			))
			Expect(spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(spec.Template.Spec.Containers[0].Name).To(Equal("compute"))
			Expect(spec.Template.Spec.SecurityContext).NotTo(BeNil())
			Expect(*spec.Template.Spec.SecurityContext.RunAsUser).To(Equal(int64(0)))
			Expect(spec.Template.Spec.ServiceAccountName).To(Equal("my-sa"))
		})

		It("should default service account name to 'default'", func() {
			reviewer.allowedBy = &corev1.ObjectReference{Name: "rawio-virt-controller"}

			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{computeContainer()},
				virtControllerSA,
				"",
				nil,
				admissionv1.Create,
			)

			handler.Handle(context.Background(), req)
			Expect(reviewer.lastReview.Spec.User).To(Equal("system:serviceaccount:test-ns:default"))
		})
	})

	Context("when pod should be skipped", func() {
		It("should allow without review when not a create operation", func() {
			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{computeContainer()},
				virtControllerSA,
				"default",
				nil,
				admissionv1.Update,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(reviewer.lastReview).To(BeNil())
		})

		It("should allow without review when not created by virt-controller", func() {
			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{computeContainer()},
				"system:serviceaccount:default:some-other-sa",
				"default",
				nil,
				admissionv1.Create,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(reviewer.lastReview).To(BeNil())
		})

		It("should allow without review when rawio annotation is absent", func() {
			req := makeValidatingRequest(
				map[string]string{},
				[]corev1.Container{computeContainer()},
				virtControllerSA,
				"default",
				nil,
				admissionv1.Create,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(reviewer.lastReview).To(BeNil())
		})

		It("should allow without review when no compute container found", func() {
			req := makeValidatingRequest(
				map[string]string{annotationRawIO: "sda"},
				[]corev1.Container{
					{Name: "sidecar", Image: "registry/sidecar:latest"},
				},
				virtControllerSA,
				"default",
				nil,
				admissionv1.Create,
			)

			resp := handler.Handle(context.Background(), req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(reviewer.lastReview).To(BeNil())
		})
	})
})

func ptrInt64(v int64) *int64 {
	return &v
}

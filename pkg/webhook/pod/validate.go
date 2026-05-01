package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	securityv1 "github.com/openshift/api/security/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

const (
	defaultVirtControllerSA = "system:serviceaccount:kubevirt:kubevirt-controller"
)

type SCCReviewer interface {
	Review(ctx context.Context, namespace string, review *securityv1.PodSecurityPolicySubjectReview) (*securityv1.PodSecurityPolicySubjectReview, error)
}

type restSCCReviewer struct {
	client rest.Interface
}

func newRESTSCCReviewer(cfg *rest.Config) (*restSCCReviewer, error) {
	scheme := runtime.NewScheme()
	if err := securityv1.Install(scheme); err != nil {
		return nil, fmt.Errorf("failed to install security scheme: %w", err)
	}

	copied := rest.CopyConfig(cfg)
	copied.GroupVersion = &securityv1.GroupVersion
	copied.APIPath = "/apis"
	copied.NegotiatedSerializer = serializer.NewCodecFactory(scheme)

	client, err := rest.RESTClientFor(copied)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST client: %w", err)
	}

	return &restSCCReviewer{client: client}, nil
}

func (r *restSCCReviewer) Review(ctx context.Context, namespace string, review *securityv1.PodSecurityPolicySubjectReview) (*securityv1.PodSecurityPolicySubjectReview, error) {
	result := &securityv1.PodSecurityPolicySubjectReview{}
	err := r.client.Post().
		Namespace(namespace).
		Resource("podsecuritypolicysubjectreviews").
		Body(review).
		Do(ctx).
		Into(result)
	return result, err
}

type ValidatingHandler struct {
	reviewer          SCCReviewer
	virtControllerSA  string
}

func NewValidatingHandler(cfg *rest.Config) (*ValidatingHandler, error) {
	reviewer, err := newRESTSCCReviewer(cfg)
	if err != nil {
		return nil, err
	}
	return newValidatingHandler(reviewer), nil
}

func newValidatingHandler(reviewer SCCReviewer) *ValidatingHandler {
	sa := os.Getenv("VIRT_CONTROLLER_SA")
	if sa == "" {
		sa = defaultVirtControllerSA
	}
	return &ValidatingHandler{
		reviewer:         reviewer,
		virtControllerSA: sa,
	}
}

func (h *ValidatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create {
		return admission.Allowed("only validating on create")
	}

	if req.UserInfo.Username != h.virtControllerSA {
		return admission.Allowed("not created by virt-controller")
	}

	pod := &corev1.Pod{}
	if err := json.Unmarshal(req.Object.Raw, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to parse pod: %w", err))
	}

	rawioValue, hasRawIO := pod.Annotations[annotationRawIO]
	if !hasRawIO || rawioValue == "" {
		return admission.Allowed("no rawio annotation")
	}

	computeIdx := -1
	for i, c := range pod.Spec.Containers {
		if c.Name == computeContainer {
			computeIdx = i
			break
		}
	}
	if computeIdx < 0 {
		return admission.Allowed("no compute container found")
	}

	compute := pod.Spec.Containers[computeIdx]
	syntheticSpec := corev1.PodSpec{
		ServiceAccountName: pod.Spec.ServiceAccountName,
		SecurityContext:    pod.Spec.SecurityContext,
		Containers: []corev1.Container{
			{
				Name:            compute.Name,
				Image:           compute.Image,
				SecurityContext: compute.SecurityContext,
			},
		},
	}

	sa := pod.Spec.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	ns := req.Namespace

	review := &securityv1.PodSecurityPolicySubjectReview{
		Spec: securityv1.PodSecurityPolicySubjectReviewSpec{
			Template: corev1.PodTemplateSpec{
				Spec: syntheticSpec,
			},
			User: fmt.Sprintf("system:serviceaccount:%s:%s", ns, sa),
			Groups: []string{
				"system:serviceaccounts",
				fmt.Sprintf("system:serviceaccounts:%s", ns),
			},
		},
	}

	result, err := h.reviewer.Review(ctx, ns, review)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError,
			fmt.Errorf("SCC review request failed: %w", err))
	}

	if result.Status.AllowedBy == nil {
		return admission.Denied(fmt.Sprintf(
			"pod service account %q does not have an SCC that permits the required capabilities (SYS_RAWIO, SETFCAP) in namespace %q",
			sa, ns))
	}

	return admission.Allowed(fmt.Sprintf("allowed by SCC %q", result.Status.AllowedBy.Name))
}

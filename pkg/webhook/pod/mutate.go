package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	annotationRawIO  = "kubevirt.io/rawioSupport"
	computeContainer = "compute"
)

type MutatingHandler struct{}

func NewMutatingHandler() *MutatingHandler {
	return &MutatingHandler{}
}

func (h *MutatingHandler) Handle(_ context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create {
		return admission.Allowed("only mutating on create")
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

	container := &pod.Spec.Containers[computeIdx]

	// virt-controller already sets root security context (RunAsUser=0, no caps
	// dropped) because the VMI webhook sets RuntimeUser=0. We only need to add
	// the rawio-specific capabilities and the setcap command.
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	if container.SecurityContext.Capabilities == nil {
		container.SecurityContext.Capabilities = &corev1.Capabilities{}
	}
	for _, cap := range []corev1.Capability{"SYS_RAWIO", "SETFCAP"} {
		if !hasCapability(container.SecurityContext.Capabilities.Add, cap) {
			container.SecurityContext.Capabilities.Add = append(
				container.SecurityContext.Capabilities.Add, cap)
		}
	}

	existingCmd := buildExistingCommand(container.Command, container.Args)
	if existingCmd != "" {
		shellCmd := "setcap cap_net_bind_service,cap_sys_rawio+ep /usr/libexec/qemu-kvm && exec " + existingCmd
		container.Command = []string{"/bin/bash", "-c", shellCmd}
		container.Args = nil
	}

	modifiedRaw, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError,
			fmt.Errorf("failed to marshal modified pod: %w", err))
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, modifiedRaw)
}

func hasCapability(caps []corev1.Capability, cap corev1.Capability) bool {
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

func buildExistingCommand(command []string, args []string) string {
	all := append(command, args...)
	if len(all) == 0 {
		return ""
	}

	var parts []string
	for _, a := range all {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

package vmi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	annotationRawIO        = "kubevirt.io/rawioSupport"
	annotationHookSidecars = "hooks.kubevirt.io/hookSidecars"
	defaultSidecarImage    = "IMAGE_PLACEHOLDER"
	envSidecarImage        = "SIDECAR_IMAGE"
)

type HookSidecar struct {
	Image           string `json:"image"`
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`
}

type MutatingHandler struct{}

func NewMutatingHandler() *MutatingHandler {
	return &MutatingHandler{}
}

func (h *MutatingHandler) Handle(_ context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create {
		return admission.Allowed("only mutating on create")
	}

	var obj map[string]any
	if err := json.Unmarshal(req.Object.Raw, &obj); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to parse VMI: %w", err))
	}

	metadata, _ := obj["metadata"].(map[string]any)
	if metadata == nil {
		return admission.Allowed("no metadata")
	}
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		return admission.Allowed("no annotations")
	}

	rawioValue, _ := annotations[annotationRawIO].(string)
	if rawioValue == "" {
		return admission.Allowed("no rawio annotation")
	}

	sidecarImage := os.Getenv(envSidecarImage)
	if sidecarImage == "" {
		sidecarImage = defaultSidecarImage
	}

	modified := false

	var sidecars []HookSidecar
	if existing, ok := annotations[annotationHookSidecars].(string); ok && existing != "" {
		if err := json.Unmarshal([]byte(existing), &sidecars); err != nil {
			return admission.Errored(http.StatusBadRequest,
				fmt.Errorf("failed to parse existing hookSidecars annotation: %w", err))
		}
	}

	hasSidecar := false
	for _, sc := range sidecars {
		if sc.Image == sidecarImage {
			hasSidecar = true
			break
		}
	}

	if !hasSidecar {
		sidecars = append(sidecars, HookSidecar{
			Image:           sidecarImage,
			ImagePullPolicy: "IfNotPresent",
		})

		sidecarsJSON, err := json.Marshal(sidecars)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError,
				fmt.Errorf("failed to marshal sidecars: %w", err))
		}

		annotations[annotationHookSidecars] = string(sidecarsJSON)
		modified = true
	}

	// Set RuntimeUser=0 so virt-controller generates a root-mode pod spec
	// and virt-handler sets up networking (tap devices) with root ownership.
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
		obj["status"] = status
	}
	runtimeUser, exists := status["runtimeUser"]
	if !exists || runtimeUser != float64(0) {
		status["runtimeUser"] = float64(0)
		modified = true
	}

	if !modified {
		return admission.Allowed("no changes needed")
	}

	modifiedRaw, err := json.Marshal(obj)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError,
			fmt.Errorf("failed to marshal modified VMI: %w", err))
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, modifiedRaw)
}

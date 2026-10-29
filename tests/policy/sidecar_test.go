package policy

import (
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	vmiwebhook "github.com/openshift-cnv/rawio-addon/pkg/webhook/vmi"
	admission "k8s.io/api/admission/v1"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime"
	webhook "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"
)

const hookKey = "hooks.kubevirt.io/hookSidecars"

type admissionCase struct {
	object  map[string]any
	old     any
	vm      bool
	allowed bool
}

// Exercise the actual rendered CEL, including the unchanged-value upgrade path.
var _ = Describe("Sidecar policy", func() {
	for _, image := range []string{
		"quay.io/test/rawio-sidecar-hook:v2.0.0",
		"registry.example:5000/rawio-hook@sha256:" + strings.Repeat("a", 64),
	} {
		Context("with image "+image, func() {
			var policy admissionv1.ValidatingAdmissionPolicy
			var binding admissionv1.ValidatingAdmissionPolicyBinding
			var variablePrograms, validations []cel.Program

			BeforeEach(func() {
				cmd := exec.Command("make", "--no-print-directory", "manifests-sidecar-policy", "SIDECAR_IMAGE="+image)
				cmd.Dir = "../.."
				manifest, err := cmd.Output()
				Expect(err).NotTo(HaveOccurred())
				docs := strings.Split(string(manifest), "\n---\n")
				Expect(docs).To(HaveLen(2), "expected policy and binding")
				policy = admissionv1.ValidatingAdmissionPolicy{}
				binding = admissionv1.ValidatingAdmissionPolicyBinding{}
				Expect(yaml.UnmarshalStrict([]byte(docs[0]), &policy)).To(Succeed())
				Expect(yaml.UnmarshalStrict([]byte(docs[1]), &binding)).To(Succeed())

				env, err := cel.NewEnv(
					cel.Variable("object", cel.DynType),
					cel.Variable("oldObject", cel.DynType),
					cel.Variable("request", cel.DynType),
					cel.Variable("variables", cel.MapType(cel.StringType, cel.DynType)),
				)
				Expect(err).NotTo(HaveOccurred())
				compile := func(expression string) cel.Program {
					GinkgoHelper()
					ast, issues := env.Compile(expression)
					Expect(issues.Err()).NotTo(HaveOccurred())
					program, err := env.Program(ast)
					Expect(err).NotTo(HaveOccurred())
					return program
				}
				variablePrograms, validations = nil, nil
				for _, variable := range policy.Spec.Variables {
					variablePrograms = append(variablePrograms, compile(variable.Expression))
				}
				for _, validation := range policy.Spec.Validations {
					validations = append(validations, compile(validation.Expression))
				}
			})

			evaluate := func(tc admissionCase) bool {
				GinkgoHelper()
				resource, operation := "virtualmachineinstances", "CREATE"
				if tc.vm {
					resource = "virtualmachines"
				}
				if tc.old != nil {
					operation = "UPDATE"
				}
				variables := map[string]any{}
				activation := map[string]any{
					"object": tc.object, "oldObject": tc.old, "variables": variables,
					"request": map[string]any{"operation": operation, "resource": map[string]any{"resource": resource}},
				}
				for i, program := range variablePrograms {
					value, _, err := program.Eval(activation)
					Expect(err).NotTo(HaveOccurred())
					variables[policy.Spec.Variables[i].Name] = value
				}
				allowed := true
				for _, program := range validations {
					value, _, err := program.Eval(activation)
					Expect(err).NotTo(HaveOccurred())
					allowed = allowed && value == types.True
				}
				return allowed
			}

			It("fails closed with a Deny binding", func() {
				Expect(policy.Spec.FailurePolicy).To(HaveValue(Equal(admissionv1.Fail)))
				Expect(binding.Spec.PolicyName).To(Equal(policy.Name))
				Expect(binding.Spec.ValidationActions).To(Equal([]admissionv1.ValidationAction{admissionv1.Deny}))
			})

			DescribeTable("type checks VM template validation against both schema shapes",
				func(schema any, typeName string) {
					env, err := cel.NewEnv(
						ext.NativeTypes(reflect.TypeOf(schema), ext.ParseStructTags(true)),
						cel.Variable("object", cel.ObjectType(typeName)),
						cel.Variable("variables", cel.MapType(cel.StringType, cel.DynType)),
					)
					Expect(err).NotTo(HaveOccurred())
					_, issues := env.Compile(policy.Spec.Validations[0].Expression)
					Expect(issues.Err()).NotTo(HaveOccurred())
				},
				Entry("VM with schemaless template metadata", schemaVM{}, "policy.schemaVM"),
				Entry("VMI without a template field", schemaVMI{}, "policy.schemaVMI"),
			)

			It("accepts the real webhook's injected annotation", func() {
				GinkgoT().Setenv("SIDECAR_IMAGE", image)
				Expect(evaluate(admissionCase{object: inject(vmi("", true))})).To(BeTrue())
			})

			approved := `[{"image":"` + image + `","imagePullPolicy":"IfNotPresent"}]`
			old := `[{"image":"quay.io/test/rawio-sidecar-hook:v1","imagePullPolicy":"IfNotPresent"}]`
			arbitrary := `[{"image":"untrusted/hook:latest"}]`
			DescribeTable("admission decisions",
				func(tc admissionCase) {
					Expect(evaluate(tc)).To(Equal(tc.allowed), "object: %v", tc.object)
				},
				Entry("no annotations", admissionCase{object: map[string]any{"metadata": map[string]any{}}, allowed: true}),
				Entry("no hooks", admissionCase{object: vmi("", false), allowed: true}),
				Entry("approved", admissionCase{object: vmi(approved, true), allowed: true}),
				Entry("rawio required", admissionCase{object: vmi(approved, false)}),
				Entry("arbitrary image", admissionCase{object: vmi(arbitrary, true)}),
				Entry("old image on create", admissionCase{object: vmi(old, true)}),
				Entry("extra hook", admissionCase{object: vmi(strings.TrimSuffix(approved, "]")+`,{"image":"other"}]`, true)}),
				Entry("override args", admissionCase{object: vmi(strings.Replace(approved, `}]`, `,"args":["evil"]}]`, 1), true)}),
				Entry("override configmap", admissionCase{object: vmi(strings.Replace(approved, `}]`, `,"configMap":{"name":"evil"}}]`, 1), true)}),
				Entry("malformed json", admissionCase{object: vmi("{", true)}),
				Entry("unchanged old image", admissionCase{object: vmi(old, true), old: vmi(old, true), allowed: true}),
				Entry("unchanged preexisting arbitrary hook", admissionCase{object: vmi(arbitrary, false), old: vmi(arbitrary, false), allowed: true}),
				Entry("changed arbitrary hook", admissionCase{object: vmi(arbitrary, true), old: vmi(old, true)}),
				Entry("new hook on update", admissionCase{object: vmi(arbitrary, true), old: vmi("", true)}),
				Entry("upgrade annotation", admissionCase{object: vmi(approved, true), old: vmi(old, true), allowed: true}),
				Entry("remove hook", admissionCase{object: vmi("", false), old: vmi(old, true), allowed: true}),
				Entry("vm rawio request", admissionCase{object: vm(""), vm: true, allowed: true}),
				Entry("vm explicit approved hook", admissionCase{object: vm(approved), vm: true}),
				Entry("vm arbitrary hook", admissionCase{object: vm(arbitrary), vm: true}),
				Entry("vm update retains hook", admissionCase{object: vm(old), old: vm(old), vm: true}),
				Entry("vm remove hook", admissionCase{object: vm(""), old: vm(old), vm: true, allowed: true}),
			)
		})
	}
})

// Minimal static type shapes reproducing the API server warnings. KubeVirt's
// VM template metadata preserves unknown fields without declaring annotations;
// a VMI spec has no template. Runtime admission still receives the full objects.
type schemaVM struct {
	Spec schemaVMSpec `cel:"spec"`
}

type schemaVMSpec struct {
	Template schemaTemplate `cel:"template"`
}

type schemaTemplate struct {
	Metadata schemaTemplateMetadata `cel:"metadata"`
}

type schemaTemplateMetadata struct{}

type schemaVMI struct {
	Spec schemaVMISpec `cel:"spec"`
}

type schemaVMISpec struct{}

func vmi(hook string, rawio bool) map[string]any {
	annotations := map[string]any{}
	if hook != "" {
		annotations[hookKey] = hook
	}
	if rawio {
		annotations["kubevirt.io/rawioSupport"] = "disk1"
	}
	return map[string]any{"metadata": map[string]any{"annotations": annotations}}
}

func vm(hook string) map[string]any {
	return map[string]any{"metadata": map[string]any{}, "spec": map[string]any{"template": vmi(hook, true)}}
}

func inject(object map[string]any) map[string]any {
	GinkgoHelper()
	raw, err := json.Marshal(object)
	Expect(err).NotTo(HaveOccurred())
	response := vmiwebhook.NewMutatingHandler().Handle(context.Background(), webhook.Request{
		AdmissionRequest: admission.AdmissionRequest{
			Operation: admission.Create,
			Object:    runtime.RawExtension{Raw: raw},
		},
	})
	Expect(response.Allowed).To(BeTrue(), "webhook denied injection: %v", response.Result)
	patchJSON, err := json.Marshal(response.Patches)
	Expect(err).NotTo(HaveOccurred())
	patch, err := jsonpatch.DecodePatch(patchJSON)
	Expect(err).NotTo(HaveOccurred())
	mutated, err := patch.Apply(raw)
	Expect(err).NotTo(HaveOccurred())
	var result map[string]any
	Expect(json.Unmarshal(mutated, &result)).To(Succeed())
	return result
}

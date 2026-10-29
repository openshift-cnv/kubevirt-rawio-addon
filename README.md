# kubevirt-rawio-addon

Enables RAW IO support for LUN disks where KubeVirt does not yet support it natively.

Upstream support is tracked at [kubevirt/enhancements#259](https://github.com/kubevirt/enhancements/issues/259). This addon bridges the gap for OpenShift Virtualization 4.20+ and upstream KubeVirt 1.6+.

## How it works

Add an annotation to your VirtualMachine to enable RAW IO on LUN disks:

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: my-vm
spec:
  template:
    metadata:
      annotations:
        kubevirt.io/rawioSupport: "datadisk1,datadisk2"
    spec:
      domain:
        devices:
          disks:
            - name: rootdisk
              disk:
                bus: virtio
            - name: datadisk1
              lun:
                bus: scsi
            - name: datadisk2
              lun:
                bus: scsi
```

The addon provides the following components:

1. **VMI mutating webhook** — injects a sidecar hook container annotation onto VMIs with `kubevirt.io/rawioSupport` and sets `RuntimeUser=0` so virt-controller generates a root-mode pod spec
2. **Pod mutating webhook** — adds `SYS_RAWIO` and `SETFCAP` capabilities to the compute container and prepends a `setcap` command to grant `qemu-kvm` the required capabilities
3. **Pod validating webhook** (OpenShift only) — validates that the pod's service account has an SCC permitting the required security context (capabilities, root execution, etc.) via the `PodSecurityPolicySubjectReview` API, similar to how OpenShift validates pods created by Deployments/ReplicaSets
4. **Sidecar hook** — an `onDefineDomain` binary (invoked by the kubevirt sidecar-shim) that sets `rawio="yes"` on matching `<disk>` elements in the libvirt domain XML
5. **SecurityContextConstraints** (OpenShift only) — allows `SYS_RAWIO` and `SETFCAP` capabilities for virt-launcher pods

## Build

```sh
make build         # build binaries
make test          # run tests
make images        # build container images
make push          # push container images
```

Override the image registry and tag:

```sh
make images IMAGE_REGISTRY=quay.io/myorg IMAGE_TAG=v1.0.0
make push IMAGE_REGISTRY=quay.io/myorg IMAGE_TAG=v1.0.0
```

## Prerequisites

The KubeVirt `Sidecar` feature gate must be enabled.

**OpenShift:**

```sh
oc annotate --overwrite -n openshift-cnv hco kubevirt-hyperconverged \
  kubevirt.kubevirt.io/jsonpatch='[{"op": "add", "path": "/spec/configuration/developerConfiguration/featureGates/-", "value": "Sidecar"}]'
```

**Upstream KubeVirt:**

```sh
kubectl patch kubevirt kubevirt -n kubevirt --type merge \
  -p '{"spec":{"configuration":{"developerConfiguration":{"featureGates":["Sidecar"]}}}}'
```

## Deploy on OpenShift

OpenShift uses its built-in service serving certificates for webhook TLS. No cert-manager required.

```sh
make deploy-openshift IMAGE_REGISTRY=quay.io/myorg IMAGE_TAG=v1.0.0
```

This installs the webhooks (mutating and validating), RBAC, and the `rawio-virt-controller` SCC.

The SCC is configured for service accounts in the `openshift-cnv` namespace. If your OpenShift Virtualization installation uses a different namespace, update the `users` field in `manifests/overlays/openshift/scc.yaml`.

### Pod service account SCC requirement

On OpenShift, the validating webhook checks that the virt-launcher pod's service account has an SCC granting the `SYS_RAWIO` and `SETFCAP` capabilities. To satisfy this:

1. Create a dedicated service account
2. Create an SCC that permits the required capabilities and bind it to the service account
3. Add a `serviceAccount` volume to the VM spec so the pod runs under that service account

Without the service account volume, the pod uses the `default` service account which typically does not have the rawio SCC, and the validating webhook will deny the pod.

See `[examples/openshift/](examples/openshift/)` for complete examples of the service account, SCC, and VM spec.

## Deploy on Kubernetes (upstream KubeVirt)

Upstream Kubernetes requires [cert-manager](https://cert-manager.io/docs/installation/) for webhook TLS certificates.

1. Install cert-manager if not already present:
  ```sh
   kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
  ```
2. Deploy the addon:
  ```sh
   make deploy-kubernetes IMAGE_REGISTRY=quay.io/myorg IMAGE_TAG=v1.0.0
  ```

This installs the webhook, RBAC, and a self-signed cert-manager Certificate for the webhook TLS.

## Optional: restrict hook sidecars to this addon

The `Sidecar` feature gate enables hook sidecars cluster-wide. Cluster administrators
can optionally install a ValidatingAdmissionPolicy to restrict new hook sidecar
configurations to this addon. This requires Kubernetes 1.30+ with
ValidatingAdmissionPolicy enabled, and applies across all namespaces.

```sh
make deploy-openshift RESTRICT_SIDECARS=true IMAGE_REGISTRY=quay.io/myorg IMAGE_TAG=v1.0.0
# Or: make deploy-kubernetes RESTRICT_SIDECARS=true ...
```

The policy rejects hook annotations in VM templates. Continue requesting RAW IO
using `spec.template.metadata.annotations["kubevirt.io/rawioSupport"]`; the webhook
injects the approved hook configuration into the VMI. New or changed VMI hook
annotations must exactly match the generated addon configuration and have a
nonempty RAW IO annotation. Additional sidecars, arguments, ConfigMap scripts,
and other overrides are rejected. Removing the VMI hook annotation is allowed.

The policy compares the entire annotation string, including JSON formatting. It
restricts the admitted configuration; it does not prove who authored it or replace
the pod service account SCC requirement. Pin `SIDECAR_IMAGE` to an image digest
when immutable image identity is required.

### Rendering and releases

The policy is separate from the default installation. Releases include an optional
`rawio-addon-sidecar-policy.yaml` generated with the release's sidecar image.
Apply that file before applying the corresponding addon release YAML.

For custom deployments or GitOps, render it using the same image settings as the addon:

```sh
make manifests-sidecar-policy IMAGE_REGISTRY=quay.io/myorg IMAGE_TAG=v1.0.0 > sidecar-policy.yaml
kubectl apply -f sidecar-policy.yaml
```

`SIDECAR_IMAGE` overrides are supported by both the addon and policy targets.
The checked-in `manifests/optional/sidecar-policy.yaml` is a template; render it
before installation.

### Upgrades and existing VMIs

On VMI updates, an unchanged hook annotation is allowed even if it references an
older addon image. Existing VMIs therefore remain updatable after an addon
upgrade; newly created VMIs receive the current sidecar. No historical image
allowlist is needed. A recreated VMI is a new admission request, so explicitly
copying an old hook annotation into it is not supported.

Keep `RESTRICT_SIDECARS=true` on subsequent deployments so the policy is updated
before the webhook. With release YAML or GitOps, preserve that same ordering.
During webhook rollout, requests injected by old webhook replicas can be rejected
until those replicas are replaced; retry creation after rollout completes.

The unchanged-value exception also preserves arbitrary sidecar configurations
that existed before policy installation. Review existing VMIs before enabling it;
the policy does not remove running sidecars. Existing VM templates containing hook
annotations must have those annotations removed before they can be updated.

Setting `RESTRICT_SIDECARS=false` does not remove an installed policy. To explicitly
disable the restriction, including after uninstalling the addon:

```sh
make undeploy-sidecar-policy
```

## Uninstall

```sh
make undeploy-openshift    # OpenShift
make undeploy-kubernetes   # upstream Kubernetes
```


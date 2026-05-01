# kubevirt-rawio-addon

Enables RAW IO support for LUN disks where KubeVirt does not yet support it natively.

Upstream support is tracked at [kubevirt/enhancements#259](https://github.com/kubevirt/enhancements/issues/259). This addon bridges the gap for OpenShift Virtualization 4.20, 4.21, and 4.22 (and their upstream KubeVirt equivalents).

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
5. **SecurityContextConstraints** (OpenShift only) — allows `SYS_RAWIO` and `SETFCAP` for virt-controller pods, with priority 11 (higher than the default kubevirt SCC)

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

## Uninstall

```sh
make undeploy-openshift    # OpenShift
make undeploy-kubernetes   # upstream Kubernetes
```


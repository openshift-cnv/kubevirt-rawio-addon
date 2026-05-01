#!/bin/bash
set -euo pipefail

PVC_NAMESPACE="${PVC_NAMESPACE:-default}"
PVC_NAME="${PVC_NAME:-scsi-rawio-pvc}"
VM_NAME="${VM_NAME:-rawio-test-vm}"
VM_SA="${VM_SA:-rawio-vm}"
CONTAINER_DISK="${CONTAINER_DISK:-quay.io/kubevirt/fedora-with-test-tooling:v20240717-a087e7e}"
SCC_NAME="${SCC_NAME:-rawio-vm}"

KUBEVIRT_NAMESPACE="openshift-cnv"
NODE=$(kubectl get pv scsi-rawio-pv -o jsonpath='{.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]}')

echo "Creating ServiceAccount $VM_SA in namespace $PVC_NAMESPACE"
kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: $VM_SA
  namespace: $PVC_NAMESPACE
EOF

echo "Creating SCC $SCC_NAME for service account $VM_SA"
kubectl apply -f - <<EOF
apiVersion: security.openshift.io/v1
kind: SecurityContextConstraints
metadata:
  name: $SCC_NAME
priority: 11
allowPrivilegedContainer: false
allowHostDirVolumePlugin: true
allowHostNetwork: false
allowHostPorts: false
allowHostPID: false
allowHostIPC: false
allowedCapabilities:
  - NET_BIND_SERVICE
  - SYS_NICE
  - SYS_RAWIO
  - SETFCAP
defaultAddCapabilities: []
seccompProfiles:
  - runtime/default
  - unconfined
  - localhost/kubevirt/kubevirt.json
volumes:
  - "*"
readOnlyRootFilesystem: false
runAsUser:
  type: RunAsAny
seLinuxContext:
  type: RunAsAny
fsGroup:
  type: RunAsAny
supplementalGroups:
  type: RunAsAny
users:
  - system:serviceaccount:${PVC_NAMESPACE}:${VM_SA}
groups: []
EOF

echo "Creating VM $VM_NAME in namespace $PVC_NAMESPACE"
echo "Node:              $NODE"
echo "PVC:               $PVC_NAME"
echo "ServiceAccount:    $VM_SA"
echo "Container disk:    $CONTAINER_DISK"

kubectl apply -f - <<EOF
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: $VM_NAME
  namespace: $PVC_NAMESPACE
  annotations:
    kubevirt.io/rawioSupport: "lun0"
spec:
  running: true
  template:
    metadata:
      annotations:
        kubevirt.io/rawioSupport: "lun0"
    spec:
      nodeSelector:
        kubernetes.io/hostname: "$NODE"
      domain:
        resources:
          requests:
            memory: 512Mi
        devices:
          rng: {}
          disks:
            - name: disk0
              disk:
                bus: virtio
            - name: lun0
              lun:
                bus: scsi
            - name: $VM_SA
              disk:
                bus: virtio
      volumes:
        - name: disk0
          containerDisk:
            image: $CONTAINER_DISK
        - name: lun0
          persistentVolumeClaim:
            claimName: $PVC_NAME
        - name: $VM_SA
          serviceAccount:
            serviceAccountName: $VM_SA
EOF

echo ""
echo "VM $VM_NAME created. Waiting for it to start..."
echo "Watch with: kubectl get vmi -n $PVC_NAMESPACE $VM_NAME -w"
echo "Console:    virtctl console -n $PVC_NAMESPACE $VM_NAME"
echo "Delete:     kubectl delete vm -n $PVC_NAMESPACE $VM_NAME"
echo "Cleanup:    kubectl delete scc $SCC_NAME; kubectl delete sa -n $PVC_NAMESPACE $VM_SA"

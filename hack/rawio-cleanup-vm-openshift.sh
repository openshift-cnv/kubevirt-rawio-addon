#!/bin/bash
set -euo pipefail

PVC_NAMESPACE="${PVC_NAMESPACE:-default}"
VM_NAME="${VM_NAME:-rawio-test-vm}"
VM_SA="${VM_SA:-rawio-vm}"
SCC_NAME="${SCC_NAME:-rawio-vm}"

echo "Deleting VM $VM_NAME in namespace $PVC_NAMESPACE"
kubectl delete vm -n "$PVC_NAMESPACE" "$VM_NAME" --ignore-not-found

echo "Deleting SCC $SCC_NAME"
kubectl delete scc "$SCC_NAME" --ignore-not-found

echo "Deleting ServiceAccount $VM_SA in namespace $PVC_NAMESPACE"
kubectl delete sa -n "$PVC_NAMESPACE" "$VM_SA" --ignore-not-found

echo "Cleanup complete."

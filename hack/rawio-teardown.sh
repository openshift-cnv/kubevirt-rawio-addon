#!/bin/bash
set -euo pipefail

if [ -z "${KUBEVIRT_NAMESPACE:-}" ]; then
    if kubectl get ns openshift-cnv &>/dev/null; then
        NAMESPACE="openshift-cnv"
    else
        NAMESPACE="kubevirt"
    fi
else
    NAMESPACE="$KUBEVIRT_NAMESPACE"
fi
PVC_NAMESPACE="${PVC_NAMESPACE:-default}"
SC_NAME="scsi-disks"
PV_NAME="scsi-rawio-pv"
PVC_NAME="scsi-rawio-pvc"

NODE=$(kubectl get pods -n "$NAMESPACE" -l kubevirt.io=virt-handler -o jsonpath='{.items[0].spec.nodeName}')
HANDLER_POD=$(kubectl get pods -n "$NAMESPACE" -l kubevirt.io=virt-handler --field-selector "spec.nodeName=$NODE" -o jsonpath='{.items[0].metadata.name}')

echo "Using node: $NODE"
echo "Using virt-handler pod: $HANDLER_POD"

exec_handler() {
    kubectl exec -n "$NAMESPACE" "$HANDLER_POD" -c virt-handler -- "$@"
}

exec_handler_chroot() {
    exec_handler /usr/bin/virt-chroot --mount /proc/1/ns/mnt exec -- "$@"
}

# Delete PVC
echo "Deleting PVC $PVC_NAME..."
kubectl delete pvc -n "$PVC_NAMESPACE" "$PVC_NAME" --ignore-not-found

# Delete PV
echo "Deleting PV $PV_NAME..."
kubectl delete pv "$PV_NAME" --ignore-not-found

# Delete StorageClass
echo "Deleting StorageClass $SC_NAME..."
kubectl delete storageclass "$SC_NAME" --ignore-not-found

# Find and delete the SCSI device
echo "Removing SCSI device..."
ADDRESS=$(exec_handler_chroot /bin/sh -c '/bin/grep -l scsi_debug /sys/bus/scsi/devices/*/model' 2>/dev/null | tr '/' '\n' | sed -n '6p' || true)

if [ -n "$ADDRESS" ]; then
    echo "Deleting SCSI device at address $ADDRESS..."
    exec_handler /usr/bin/echo 1 ">" "/proc/1/root/sys/class/scsi_device/${ADDRESS}/device/delete"
fi

# Unload scsi_debug module
echo "Unloading scsi_debug module..."
exec_handler_chroot /usr/sbin/modprobe -r scsi_debug || echo "WARNING: Failed to unload scsi_debug (device may still be in use)"

echo ""
echo "=== Teardown complete ==="

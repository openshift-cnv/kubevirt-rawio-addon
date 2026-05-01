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

# Find a worker node with a virt-handler pod
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

# Load scsi_debug kernel module
echo "Loading scsi_debug module..."
exec_handler_chroot /usr/sbin/modprobe scsi_debug dev_size_mb=8

# Wait for the device to appear and find its address/path
echo "Waiting for SCSI device..."
for i in $(seq 1 20); do
    MODEL_PATH=$(exec_handler_chroot /bin/sh -c '/bin/grep -l scsi_debug /sys/bus/scsi/devices/*/model' 2>/dev/null || true)
    if [ -n "$MODEL_PATH" ]; then
        break
    fi
    sleep 1
done

if [ -z "$MODEL_PATH" ]; then
    echo "ERROR: scsi_debug device did not appear"
    exit 1
fi

ADDRESS=$(echo "$MODEL_PATH" | tr '/' '\n' | sed -n '6p')
BLOCK_DEV=$(exec_handler_chroot /bin/ls "/sys/bus/scsi/devices/${ADDRESS}/block" | tr -d '[:space:]')
DEVICE="/dev/${BLOCK_DEV}"

echo "SCSI device address: $ADDRESS"
echo "Block device: $DEVICE"

# Create StorageClass
echo "Creating StorageClass $SC_NAME..."
kubectl apply -f - <<EOF
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: $SC_NAME
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: WaitForFirstConsumer
EOF

# Create namespace if it doesn't exist
kubectl get ns "$PVC_NAMESPACE" >/dev/null 2>&1 || kubectl create ns "$PVC_NAMESPACE"

# Create PV
echo "Creating PV $PV_NAME..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolume
metadata:
  name: $PV_NAME
spec:
  capacity:
    storage: 8Mi
  storageClassName: $SC_NAME
  volumeMode: Block
  accessModes:
    - ReadWriteOnce
  nodeAffinity:
    required:
      nodeSelectorTerms:
        - matchExpressions:
            - key: kubernetes.io/hostname
              operator: In
              values:
                - "$NODE"
  local:
    path: "$DEVICE"
EOF

# Create PVC
echo "Creating PVC $PVC_NAME in namespace $PVC_NAMESPACE..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $PVC_NAME
  namespace: $PVC_NAMESPACE
spec:
  storageClassName: $SC_NAME
  volumeMode: Block
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 8Mi
EOF

echo ""
echo "=== Setup complete ==="
echo "Node:           $NODE"
echo "SCSI address:   $ADDRESS"
echo "Device:         $DEVICE"
echo "StorageClass:   $SC_NAME"
echo "PV:             $PV_NAME"
echo "PVC:            $PVC_NAMESPACE/$PVC_NAME"
echo ""
echo "To tear down, run: ./hack/rawio-teardown.sh"

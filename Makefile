IMAGE_REGISTRY ?= quay.io/openshift-cnv
IMAGE_TAG ?= latest
WEBHOOK_IMAGE ?= $(IMAGE_REGISTRY)/rawio-webhook:$(IMAGE_TAG)
SIDECAR_IMAGE ?= $(IMAGE_REGISTRY)/rawio-sidecar-hook:$(IMAGE_TAG)
KUBECTL ?= $(shell which oc 2>/dev/null || which kubectl)

.PHONY: build test image-webhook image-sidecar images push deploy-openshift deploy-kubernetes undeploy-openshift undeploy-kubernetes clean

build:
	go build -o bin/webhook ./cmd/webhook/
	go build -o bin/sidecar-hook ./cmd/sidecar-hook/

test:
	go test ./...

image-webhook:
	podman build -f Dockerfile.webhook -t $(WEBHOOK_IMAGE) .

image-sidecar:
	podman build -f Dockerfile.sidecar-hook -t $(SIDECAR_IMAGE) .

images: image-webhook image-sidecar

push:
	podman push $(WEBHOOK_IMAGE)
	podman push $(SIDECAR_IMAGE)

deploy-openshift:
	$(KUBECTL) kustomize manifests/overlays/openshift | \
		sed 's|rawio-webhook:latest|$(WEBHOOK_IMAGE)|g; s|rawio-sidecar-hook:latest|$(SIDECAR_IMAGE)|g' | \
		$(KUBECTL) apply -f -

deploy-kubernetes:
	$(KUBECTL) kustomize manifests/overlays/kubernetes | \
		sed 's|rawio-webhook:latest|$(WEBHOOK_IMAGE)|g; s|rawio-sidecar-hook:latest|$(SIDECAR_IMAGE)|g' | \
		$(KUBECTL) apply -f -

undeploy-openshift:
	$(KUBECTL) delete -k manifests/overlays/openshift --ignore-not-found

undeploy-kubernetes:
	$(KUBECTL) delete -k manifests/overlays/kubernetes --ignore-not-found

clean:
	rm -rf bin/

tag ?= v1
image ?= webhook

.PHONY: clean all build docker-build
all: docker-build
webhook: webhook.go main.go
	dep ensure
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o webhook .
image.tar: Dockerfile webhook
	docker build --no-cache -t $(image):$(tag) .
	docker save -o image.tar $(image):$(tag)
build: webhook
docker-build: image.tar
clean:
	rm -f webhook image.tar

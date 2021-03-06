VERSION := $(shell cat ../version.txt)
# Image URL to use all building/pushing image targets
IMG ?= seldonio/seldon-core-executor:${VERSION}

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...


# Build manager binary
executor: fmt vet
	go build -o executor main.go


.PHONY: copy_protos
copy_protos:
	cp -r ../proto/tensorflow/tensorflow/** proto/tensorflow


.PHONY: compile_seldon_proto
compile_seldon_proto:
	cp ../proto/prediction.proto api/grpc
	cd api/grpc && protoc -I. -I${GOPATH}/src/github.com/tensorflow/tensorflow --go_out=paths=source_relative,plugins=grpc:. prediction.proto
	rm api/grpc/prediction.proto

# https://github.com/tensorflow/serving/issues/1365#issuecomment-525351995
.PHONY: compile_tensorflow_proto
compile_tensorflow_proto:
	git clone -b r1.15 https://github.com/tensorflow/tensorflow.git
	git clone -b r1.14 https://github.com/tensorflow/serving.git
	go run protoc.go
	go mod edit -replace=github.com/tensorflow/tensorflow/tensorflow/go/core=./proto/tensorflow/core
	cd proto/tensorflow/core && go mod init github.com/tensorflow/tensorflow/tensorflow/go/core && cd -
	go build ./proto/tensorflow/serving

.PHONY: add_protos
add_protos:
	cd tensorflow && find ./tensorflow -name '*.proto' | cpio -pdm ../proto
	cd serving && find ./tensorflow_serving -name '*.proto' | cpio -pdm ../proto

# Run tests
test: fmt vet
	go test ./api/... ./predictor/... ./k8s/...  -coverprofile cover.out

# Build the docker image
docker-build: test
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}


kind-image-install: docker-build
	kind load -v 3 docker-image ${IMG}


.PHONY: clean
clean:
	rm -rf vendor
	rm -rf tensorflow
	rm -rf serving

licenses/dep.txt:
	go list -m all | cut -d ' ' -f 1 > licenses/dep.txt


PKG=github.com/sercand/grpc-angular
ANGULAR_PLUGIN_PKG=$(PKG)/protoc-gen-angular

build:
	go build -o bin/protoc-gen-angular $(ANGULAR_PLUGIN_PKG)
test:
	go test -v github.com/sercand/grpc-angular/protoc-gen-angular/genangular

.PHONY: build test

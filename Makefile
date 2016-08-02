PKG=github.com/sercand/grpc-angular
ANGULAR_PLUGIN_PKG=$(PKG)/protoc-gen-angular
AUTH_PLUGIN_PKG=$(PKG)/protoc-gen-otsimoauth

build:
	go build -o bin/protoc-gen-angular $(ANGULAR_PLUGIN_PKG)
	sh bin/run.sh bin
test:
	go test -v github.com/sercand/grpc-angular/protoc-gen-angular/genangular

.PHONY: build test

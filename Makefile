all: build

TAG?=v0.1.0
REGISTRY?=ihub.helium.io:29006
FLAGS=
ENVVAR=
GOOS?=linux
ROOTPATH=`pwd` 
BUILDGOPATH=/tmp/k8splugin-build
BUILDPATH=$(BUILDGOPATH)/src/k8s-plugins/extender-scheduler
MASTERS?="127.0.0.1"
BINMOVEPATH="/opt/bin"
SVCMOVEPATH="/etc/systemd/system/"
MASTERUSER?=root
 
.IGNORE : buildEnvClean
.IGNORE : deletedeploy
.IGNORE : deletedeploy-nodeselector

deps:
	@go get github.com/tools/godep
	
buildEnvClean:
	@rm $(BUILDPATH) 1>/dev/null 2>/dev/null || true

buildEnv: buildEnvClean
	@mkdir -p $(BUILDGOPATH)/src/k8s-plugins/ 1>/dev/null 2>/dev/null
	@ln -s $(ROOTPATH) $(BUILDPATH)
	
build: buildEnv clean deps 
	@cd $(BUILDPATH) && GOPATH=$(BUILDGOPATH) $(ENVVAR) GOOS=$(GOOS) CGO_ENABLED=0   godep go build ./...
	@cd $(BUILDPATH) && GOPATH=$(BUILDGOPATH) $(ENVVAR) GOOS=$(GOOS) CGO_ENABLED=0   godep go build -o enndata-scheduler pkg/main.go

docker:
ifndef REGISTRY
	ERR = $(error REGISTRY is undefined)
	$(ERR)
endif
	docker build --pull -t ${REGISTRY}/library/enndata-scheduler:${TAG} .
	docker push ${REGISTRY}/library/enndata-scheduler:${TAG}
	docker build -t ${REGISTRY}/library/k8s-scheduler:v1.11.2 ./k8s-scheduler/
	docker push ${REGISTRY}/library/k8s-scheduler:v1.11.2

deletedeploy:
	@kubectl delete -f deploy/enndata-scheduler.yaml 1>/dev/null 2>/dev/null || true
	
deletedeploy-nodeselector:
	@kubectl delete -f deploy/nsnodeselector-server.yaml 1>/dev/null 2>/dev/null || true

install: deletedeploy
	./gencerts.sh
	@cat deploy/enndata-scheduler.yaml | sed "s/ihub.helium.io:29006/$(REGISTRY)/g" > deploy/tmp.yaml
	kubectl create -f deploy/tmp.yaml
	@rm deploy/tmp.yaml
	
install-nsnodeselector: deletedeploy-nodeselector
	./gencerts.sh
	@cat deploy/nsnodeselector-server.yaml | sed "s/ihub.helium.io:29006/$(REGISTRY)/g" > deploy/tmp.yaml
	kubectl create -f deploy/tmp.yaml
	@rm deploy/tmp.yaml

systemd: build
	# @rm -rf tls-certs
	#./gencerts.sh false localhost
	./systemd.sh $(BINMOVEPATH) $(SVCMOVEPATH) $(MASTERUSER) $(MASTERS)
	@rm enndata-scheduler
	@rm -rf tls-certs
	
uninstall: deletedeploy

release: build docker
	rm -f enndata-scheduler

clean: buildEnvClean
	@rm -f enndata-scheduler

format:
	test -z "$$(find . -path ./vendor -prune -type f -o -name '*.go' -exec gofmt -s -d {} + | tee /dev/stderr)" || \
	test -z "$$(find . -path ./vendor -prune -type f -o -name '*.go' -exec gofmt -s -w {} + | tee /dev/stderr)"
 
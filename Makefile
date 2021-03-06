.PHONY: \
	lint \
	vet \
	fmt \
	fmtcheck \
	testdeps \
	pretest \
	gotest \
	test \

CI_TOKEN = foo

lint:
	@ go get -v github.com/golang/lint/golint
	[ -z "$$(golint . | grep -v 'type name will be used as docker.DockerInfo' | grep -v 'context.Context should be the first' | tee /dev/stderr)" ]

vet:
	go vet $$(go list ./... | grep -v vendor)

fmt:
	gofmt -s -w $$(go list ./... | grep -v vendor)

fmtcheck:
	[ -z "$$(gofmt -s -d $$(go list ./... | grep -v vendor) | tee /dev/stderr)" ]

testdeps:
	go get -u github.com/golang/dep/cmd/dep
	dep ensure -v

pretest: testdeps lint vet fmtcheck

gotest:
	go test -race $$(go list ./... | grep -v vendor) -cover

test: pretest gotest

test-short:	testdeps gotest

test-ci: pretest
	go test -race $$(go list ./... | grep -v vendor) -cover -coverprofile=.coverprofile .
	goveralls -coverprofile=.coverprofile -repotoken ${CI_TOKEN} -coverprofile=.profile.cov

test-travis: pretest
	sh scripts/coverall.sh
	goveralls -coverprofile=.coverprofile -repotoken ${CI_TOKEN} -coverprofile=.profile.cov


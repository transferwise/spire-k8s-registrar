module github.com/transferwise/spire-k8s-registrar

go 1.13

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.8.0
	github.com/onsi/gomega v1.5.0
	github.com/spiffe/go-spiffe v0.0.0-20200115174642-4e401e3b85fe
	github.com/spiffe/spire/proto/spire v0.9.2
	google.golang.org/grpc v1.24.0
	k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
	sigs.k8s.io/controller-runtime v0.4.0
)

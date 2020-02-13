module github.com/transferwise/spire-k8s-registrar

go 1.13

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/go-logr/logr v0.1.0
	github.com/hashicorp/hcl v1.0.0
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/spiffe/go-spiffe v0.0.0-20200115174642-4e401e3b85fe
	github.com/spiffe/spire/proto/spire v0.9.2
	github.com/zeebo/errs v1.2.2
	google.golang.org/grpc v1.24.0
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
	sigs.k8s.io/controller-runtime v0.4.0
	sigs.k8s.io/kustomize/kustomize/v3 v3.5.4 // indirect
)

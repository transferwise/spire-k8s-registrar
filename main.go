/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/spiffe/spire/proto/spire/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/url"
	"os"
	"path"

	"github.com/go-logr/logr"
	"github.com/spiffe/spire/proto/spire/api/registration"

	"github.com/spiffe/go-spiffe/spiffe"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/transferwise/spire-k8s-registrar/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	scheme     = runtime.NewScheme()
	setupLog   = ctrl.Log.WithName("setup")
	configFlag = flag.String("config", "spire-k8s-registrar.conf", "configuration file")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = corev1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	fmt.Println("Parsing config")
	flag.Parse()

	config, err := LoadConfig(*configFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(func(o *zap.Options) {
		o.Development = true
	}))

	//Connect to Spire Server
	spireClient, err := ConnectSpire(context.Background(), setupLog, config.ServerAddress, config.ServerSocketPath)
	if err != nil {
		setupLog.Error(err, "unable to connect to spire server")
		os.Exit(1)
	}
	setupLog.Info("Connected to spire server.")

	myId, err := makeMyId(context.Background(), setupLog, spireClient, config.Cluster, config.TrustDomain)
	if err != nil {
		setupLog.Error(err, "unable to create parent ID")
		os.Exit(1)
	}

	// Setup all Controllers
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: config.MetricsAddr,
		LeaderElection:     config.LeaderElection,
		Port:               9443,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = controllers.NewNodeReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("Node"),
		mgr.GetScheme(),
		config.TrustDomain,
		myId,
		spireClient,
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Node")
		os.Exit(1)
	}

	mode := controllers.PodReconcilerModeServiceAccount
	value := ""
	if len(config.PodLabel) > 0 {
		mode = controllers.PodReconcilerModeLabel
		value = config.PodLabel
	}
	if len(config.PodAnnotation) > 0 {
		mode = controllers.PodReconcilerModeAnnotation
		value = config.PodAnnotation
	}
	if err = controllers.NewPodReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("Pod"),
		mgr.GetScheme(),
		config.TrustDomain,
		myId,
		spireClient,
		mode,
		value,
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type SpiffeLogWrapper struct {
	delegate logr.Logger
}

func (slw SpiffeLogWrapper) Debugf(format string, args ...interface{}) {
	slw.delegate.V(1).Info(fmt.Sprintf(format, args...))
}
func (slw SpiffeLogWrapper) Infof(format string, args ...interface{}) {
	slw.delegate.Info(fmt.Sprintf(format, args...))
}
func (slw SpiffeLogWrapper) Warnf(format string, args ...interface{}) {
	slw.delegate.Info(fmt.Sprintf(format, args...))
}
func (slw SpiffeLogWrapper) Errorf(format string, args ...interface{}) {
	slw.delegate.Info(fmt.Sprintf(format, args...))
}

func ConnectSpire(ctx context.Context, log logr.Logger, serverAddress, serverSocketPath string) (registration.RegistrationClient, error) {

	var conn *grpc.ClientConn
	var err error

	if serverAddress != "" {
		tlsPeer, err := spiffe.NewTLSPeer(spiffe.WithWorkloadAPIAddr("unix://"+serverSocketPath), spiffe.WithLogger(SpiffeLogWrapper{log}))
		if err != nil {
			return nil, err
		}
		conn, err = tlsPeer.DialGRPC(ctx, serverAddress, spiffe.ExpectAnyPeer())
		if err != nil {
			return nil, err
		}
	} else {
		fmt.Printf("Connecting to: %s\n", "unix://"+serverSocketPath)
		conn, err = grpc.DialContext(ctx, "unix://"+serverSocketPath, grpc.WithInsecure())
		if err != nil {
			return nil, err
		}
	}
	spireClient := registration.NewRegistrationClient(conn)
	return spireClient, nil
}

// ServerURI creates a server SPIFFE URI given a trustDomain.
func ServerURI(trustDomain string) *url.URL {
	return &url.URL{
		Scheme: "spiffe",
		Host:   trustDomain,
		Path:   path.Join("spire", "server"),
	}
}

// ServerID creates a server SPIFFE ID string given a trustDomain.
func ServerID(trustDomain string) string {
	return ServerURI(trustDomain).String()
}

func makeID(trustDomain string, pathFmt string, pathArgs ...interface{}) string {
	id := url.URL{
		Scheme: "spiffe",
		Host:   trustDomain,
		Path:   path.Clean(fmt.Sprintf(pathFmt, pathArgs...)),
	}
	return id.String()
}

func nodeID(trustDomain string, cluster string) string {
	return makeID(trustDomain, "spire-k8s-operatorz/%s/node", cluster)
}

func makeMyId(ctx context.Context, reqLogger logr.Logger, spireClient registration.RegistrationClient, cluster string, trustDomain string) (string, error) {
	myId := nodeID(trustDomain, cluster)
	reqLogger.Info("Initializing operator parent ID.")
	_, err := spireClient.CreateEntry(ctx, &common.RegistrationEntry{
		Selectors: []*common.Selector{
			{Type: "k8s_psat", Value: fmt.Sprintf("cluster:%s", cluster)},
		},
		ParentId: ServerID(trustDomain),
		SpiffeId: myId,
	})
	if err != nil {
		if status.Code(err) != codes.AlreadyExists {
			reqLogger.Info("Failed to create operator parent ID", "spiffeID", myId)
			return "", err
		}
	}
	reqLogger.Info("Initialized operator parent ID", "spiffeID", myId)
	return myId, nil
}

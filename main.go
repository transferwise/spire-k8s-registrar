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
	"github.com/go-logr/logr"
	"github.com/spiffe/spire/proto/spire/api/registration"
	"os"

	"github.com/spiffe/go-spiffe/spiffe"
	spiffeidv1beta1 "github.com/transferwise/spire-k8s-registrar/api/v1beta1"
	"github.com/transferwise/spire-k8s-registrar/controllers"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = spiffeidv1beta1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var spireHost string
	var trustDomain string
	var cluster string
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&spireHost, "spire-server", "", "Host and port of the spire server to connect to")
	flag.StringVar(&trustDomain, "trust-domain", "", "Spire trust domain to create IDs for")
	flag.StringVar(&cluster, "cluster", "", "Cluster name as configured for psat attestor")
	flag.Parse()

	ctrl.SetLogger(zap.New(func(o *zap.Options) {
		o.Development = true
	}))

	if len(spireHost) <= 0 {
		setupLog.Error(fmt.Errorf("--spire-server flag must be provided"), "")
		os.Exit(1)
	}

	if len(cluster) <= 0 {
		setupLog.Error(fmt.Errorf("--cluster flag must be provided"), "")
		os.Exit(1)
	}

	if len(trustDomain) <= 0 {
		setupLog.Error(fmt.Errorf("--trust-domain flag must be provided"), "")
		os.Exit(1)
	}

	// Setup all Controllers
	spireClient, err := ConnectSpire(context.Background(), setupLog, spireHost)
	if err != nil {
		setupLog.Error(err, "unable to connect to spire server")
		os.Exit(1)
	}
	setupLog.Info("Connected to spire server.")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		Port:               9443,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.ClusterSpiffeIDReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("ClusterSpiffeID"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterSpiffeID")
		os.Exit(1)
	}
	if err = (&controllers.SpireEntryReconciler{
		Client:      mgr.GetClient(),
		Log:         ctrl.Log.WithName("controllers").WithName("SpireEntry"),
		Scheme:      mgr.GetScheme(),
		SpireClient: spireClient,
		Cluster:     cluster,
		TrustDomain: trustDomain,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SpireEntry")
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

func ConnectSpire(ctx context.Context, log logr.Logger, serviceName string) (registration.RegistrationClient, error) {

	tlsPeer, err := spiffe.NewTLSPeer(spiffe.WithWorkloadAPIAddr("unix:///run/spire/sockets/agent.sock"), spiffe.WithLogger(SpiffeLogWrapper{log}))
	if err != nil {
		return nil, err
	}
	conn, err := tlsPeer.DialGRPC(ctx, serviceName, spiffe.ExpectAnyPeer())
	if err != nil {
		return nil, err
	}
	spireClient := registration.NewRegistrationClient(conn)
	return spireClient, nil
}

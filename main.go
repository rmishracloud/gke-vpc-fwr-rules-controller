package main

import (
	"context"
	"flag"
	"os"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	container "cloud.google.com/go/container/apiv1"
	"github.com/rmishracloud/gke-vpc-fwr-rules-controller/internal/config"
	"github.com/rmishracloud/gke-vpc-fwr-rules-controller/internal/controller"
	"github.com/rmishracloud/gke-vpc-fwr-rules-controller/internal/gcp"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var syncPeriod time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.DurationVar(&syncPeriod, "sync-period", 20*time.Minute, "Periodic resync interval for reconciling firewall rule drift.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	setupLog := ctrl.Log.WithName("setup")

	meta := gcp.NewMetadataProvider()

	projectID, err := meta.ProjectID()
	if err != nil {
		setupLog.Error(err, "failed to get project ID from metadata server")
		os.Exit(1)
	}

	zone, err := meta.Zone()
	if err != nil {
		setupLog.Error(err, "failed to get zone from metadata server")
		os.Exit(1)
	}

	region, err := gcp.RegionFromZone(zone)
	if err != nil {
		setupLog.Error(err, "failed to derive region from zone", "zone", zone)
		os.Exit(1)
	}

	clusterName, err := meta.ClusterName()
	if err != nil {
		setupLog.Error(err, "failed to get cluster name from metadata server")
		os.Exit(1)
	}

	setupLog.Info("discovered cluster identity", "project", projectID, "zone", zone, "region", region, "cluster", clusterName)

	ctx := context.Background()

	containerClient, err := container.NewClusterManagerClient(ctx)
	if err != nil {
		setupLog.Error(err, "failed to create GKE container client")
		os.Exit(1)
	}
	defer containerClient.Close()

	clusterInfo, err := gcp.GetClusterInfo(ctx, containerClient, projectID, zone, clusterName)
	if err != nil {
		setupLog.Error(err, "failed to get cluster info")
		os.Exit(1)
	}

	setupLog.Info("discovered cluster info",
		"network", clusterInfo.Network,
		"podCIDR", clusterInfo.PodCIDR,
		"hostProject", clusterInfo.HostProject,
	)

	cfg := &config.Config{
		Project:     projectID,
		Zone:        zone,
		Region:      region,
		ClusterName: clusterName,
		Network:     clusterInfo.Network,
		PodCIDR:     clusterInfo.PodCIDR,
		HostProject: clusterInfo.HostProject,
	}

	firewallsClient, err := compute.NewFirewallsRESTClient(ctx)
	if err != nil {
		setupLog.Error(err, "failed to create firewalls client")
		os.Exit(1)
	}
	defer firewallsClient.Close()

	subnetworksClient, err := compute.NewSubnetworksRESTClient(ctx)
	if err != nil {
		setupLog.Error(err, "failed to create subnetworks client")
		os.Exit(1)
	}
	defer subnetworksClient.Close()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "gke-vpc-fwr-controller.rmishracloud.github.com",
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
		},
	})

	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	reconciler := &controller.GatewayReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		FirewallMgr:  gcp.NewFirewallManager(firewallsClient),
		SubnetClient: subnetworksClient,
		Config:       cfg,
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

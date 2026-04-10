package controller

import (
	"context"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"github.com/rmishracloud/gke-vpc-fwr-rules-controller/internal/config"
	"github.com/rmishracloud/gke-vpc-fwr-rules-controller/internal/gcp"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var matchingGatewayClasses = map[gatewayv1.ObjectName]struct{}{
	"gke-l7-rilb":                      {},
	"gke-l7-regional-external-managed": {},
}

type GatewayReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	FirewallMgr  *gcp.FirewallManager
	SubnetClient *compute.SubnetworksClient
	Config       *config.Config

	mu              sync.Mutex
	proxySubnetCIDR string
	cacheTime       time.Time
}

const cacheTTL = 5 * time.Minute

func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var gatewayList gatewayv1.GatewayList
	if err := r.List(ctx, &gatewayList); err != nil {
		return ctrl.Result{}, err
	}

	matchCount := 0
	for i := range gatewayList.Items {
		gw := &gatewayList.Items[i]
		if _, ok := matchingGatewayClasses[gw.Spec.GatewayClassName]; ok {
			matchCount++
		}
	}

	firewallName := gcp.FirewallRuleName(r.Config.ClusterName)

	if matchCount > 0 {
		proxySubnetCIDR, err := r.getProxySubnetCIDR(ctx)
		if err != nil {
			logger.Error(err, "failed to discover proxy-only subnet CIDR")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}

		params := gcp.FirewallParams{
			Name:         firewallName,
			Network:      r.Config.Network,
			SourceRanges: []string{proxySubnetCIDR},
			DestRanges:   []string{r.Config.PodCIDR},
			Project:      r.Config.HostProject,
		}

		if err := r.FirewallMgr.EnsureFirewallRule(ctx, params); err != nil {
			logger.Error(err, "failed to ensure firewall rule", "name", firewallName)
			return ctrl.Result{}, err
		}
		logger.Info("firewall rule ensured for proxy-only subnet", "name", firewallName, "matchingGateways", matchCount)
		logger.Info("creating rule for healthcheck ranges", "name", firewallName+"-hc", "matchingGateways", matchCount)

		params = gcp.FirewallParams{
			Name:         firewallName + "-hc",
			Network:      r.Config.Network,
			SourceRanges: []string{"35.191.0.0/16", "130.211.0.0/22"},
			DestRanges:   []string{r.Config.PodCIDR},
			Project:      r.Config.HostProject,
		}

		if err := r.FirewallMgr.EnsureFirewallRule(ctx, params); err != nil {
			logger.Error(err, "failed to ensure firewall rule for health check ranges", "name", firewallName+"-hc")
			return ctrl.Result{}, err
		}
		logger.Info("firewall rules for healthcheck ranges ensured", "name", firewallName+"-hc", "matchingGateways", matchCount)

	} else {
		if err := r.FirewallMgr.DeleteFirewallRule(ctx, r.Config.HostProject, firewallName); err != nil {
			logger.Error(err, "failed to delte firewall rule for proxy subnets", "name", firewallName)
			return ctrl.Result{}, err
		}
		if err := r.FirewallMgr.DeleteFirewallRule(ctx, r.Config.HostProject, firewallName+"-hc"); err != nil {
			logger.Error(err, "failed to delte firewall rule for health checks", "name", firewallName)
			return ctrl.Result{}, err
		}
		logger.Info("no matching gateways, firewall rules removed", "name", firewallName)
	}

	return ctrl.Result{}, nil
}

func (r *GatewayReconciler) getProxySubnetCIDR(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.proxySubnetCIDR != "" && time.Since(r.cacheTime) < cacheTTL {
		return r.proxySubnetCIDR, nil
	}

	cidr, err := gcp.GetProxyOnlySubnetCIDR(ctx, r.SubnetClient, r.Config.HostProject, r.Config.Region, r.Config.Network)
	if err != nil {
		return "", err
	}

	r.proxySubnetCIDR = cidr
	r.cacheTime = time.Now()
	return cidr, nil
}

func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}).
		Named("gateway-firewall").
		Complete(r)
}

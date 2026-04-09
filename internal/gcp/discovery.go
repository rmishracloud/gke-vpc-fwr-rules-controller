package gcp

import (
	"context"
	"fmt"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"google.golang.org/api/iterator"
)

// ClusterInfo holds the network and pod CIDR information for a GKE cluster.
type ClusterInfo struct {
	Network     string // Full network self-link
	PodCIDR     string // Cluster pod CIDR
	HostProject string // Host project parsed from network self-link
}

// GetClusterInfo fetches network, pod CIDR, and host project from the GKE API.
func GetClusterInfo(ctx context.Context, client *container.ClusterManagerClient, project, location, clusterName string) (*ClusterInfo, error) {
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, clusterName)
	cluster, err := client.GetCluster(ctx, &containerpb.GetClusterRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("getting cluster %s: %w", name, err)
	}

	networkSelfLink := cluster.GetNetworkConfig().GetNetwork()
	if networkSelfLink == "" {
		networkSelfLink = fmt.Sprintf("projects/%s/global/networks/%s", project, cluster.GetNetwork())
	}

	hostProject, err := parseProjectFromSelfLink(networkSelfLink)
	if err != nil {
		hostProject = project
	}

	podCIDR := cluster.GetClusterIpv4Cidr()
	if podCIDR == "" {
		podCIDR = cluster.GetIpAllocationPolicy().GetClusterIpv4CidrBlock()
	}
	if podCIDR == "" {
		return nil, fmt.Errorf("could not determine pod CIDR for cluster %s", clusterName)
	}

	return &ClusterInfo{
		Network:     networkSelfLink,
		PodCIDR:     podCIDR,
		HostProject: hostProject,
	}, nil
}

// GetProxyOnlySubnetCIDR finds the REGIONAL_MANAGED_PROXY subnet in the given network and region.
func GetProxyOnlySubnetCIDR(ctx context.Context, client *compute.SubnetworksClient, hostProject, region, networkURL string) (string, error) {
	it := client.List(ctx, &computepb.ListSubnetworksRequest{
		Project: hostProject,
		Region:  region,
	})

	for {

		subnet, err := it.Next()
		fmt.Println("subnet:::::>>>", subnet.Name)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", fmt.Errorf("listing subnets: %w", err)
		}

		if subnet.GetPurpose() == "REGIONAL_MANAGED_PROXY" && subnet.GetNetwork() == networkURL {
			return subnet.GetIpCidrRange(), nil
		}
	}

	return "", fmt.Errorf("no proxy-only subnet (purpose=REGIONAL_MANAGED_PROXY) found in network %s region %s", networkURL, region)
}

// parseProjectFromSelfLink extracts the project ID from a resource self-link.
// Expected format: projects/{project}/global/networks/{name} or
// https://www.googleapis.com/compute/v1/projects/{project}/global/networks/{name}
func parseProjectFromSelfLink(selfLink string) (string, error) {
	parts := strings.Split(selfLink, "/")
	for i, part := range parts {
		if part == "projects" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("could not parse project from self-link: %s", selfLink)
}

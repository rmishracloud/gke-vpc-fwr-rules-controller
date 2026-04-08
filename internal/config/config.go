package config

// Config holds the runtime configuration for the controller,
// populated at startup from GCP metadata and APIs.
type Config struct {
	// Project is the GCP project where the GKE cluster runs (service project).
	Project string
	// Zone is the GKE cluster zone.
	Zone string
	// Region is the GCP region, derived from Zone.
	Region string
	// ClusterName is the GKE cluster name.
	ClusterName string
	// Network is the full VPC network self-link (e.g., projects/host-proj/global/networks/my-vpc).
	Network string
	// PodCIDR is the cluster's pod IP range.
	PodCIDR string
	// HostProject is the shared VPC host project where firewall rules are managed.
	HostProject string
}

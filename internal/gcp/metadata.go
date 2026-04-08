package gcp

import (
	"fmt"
	"strings"

	"cloud.google.com/go/compute/metadata"
)

// MetadataProvider reads identity information from the GCP metadata server.
type MetadataProvider struct{}

// NewMetadataProvider returns a new MetadataProvider.
func NewMetadataProvider() *MetadataProvider {
	return &MetadataProvider{}
}

// ProjectID returns the GCP project ID from the metadata server.
func (m *MetadataProvider) ProjectID() (string, error) {
	return metadata.ProjectID()
}

// Zone returns the instance zone from the metadata server.
func (m *MetadataProvider) Zone() (string, error) {
	return metadata.Zone()
}

// ClusterName returns the GKE cluster name from instance attributes.
func (m *MetadataProvider) ClusterName() (string, error) {
	return metadata.InstanceAttributeValue("cluster-name")
}

// RegionFromZone derives the region from a zone string (e.g., "us-central1-a" -> "us-central1").
func RegionFromZone(zone string) (string, error) {
	lastDash := strings.LastIndex(zone, "-")
	if lastDash == -1 {
		return "", fmt.Errorf("invalid zone format: %s", zone)
	}
	return zone[:lastDash], nil
}

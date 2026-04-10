package gcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/protobuf/proto"
)

// FirewallParams defines the parameters for a firewall rule.
type FirewallParams struct {
	Name         string
	Network      string
	SourceRanges []string
	DestRanges   []string
	Project      string
}

// FirewallManager manages GCP firewall rules.
type FirewallManager struct {
	client *compute.FirewallsClient
}

// NewFirewallManager creates a new FirewallManager.
func NewFirewallManager(client *compute.FirewallsClient) *FirewallManager {
	return &FirewallManager{client: client}
}

// EnsureFirewallRule creates or updates the firewall rule. Idempotent.
func (f *FirewallManager) EnsureFirewallRule(ctx context.Context, p FirewallParams) error {
	existing, err := f.client.Get(ctx, &computepb.GetFirewallRequest{
		Project:  p.Project,
		Firewall: p.Name,
	})
	if err != nil {
		if isNotFound(err) {
			return f.insertFirewall(ctx, p)
		}
		return fmt.Errorf("getting firewall rule %s: %w", p.Name, err)
	}

	if needsUpdate(existing, p) {
		return f.patchFirewall(ctx, p)
	}
	return nil
}

// DeleteFirewallRule removes the firewall rule. No-op if it doesn't exist.
func (f *FirewallManager) DeleteFirewallRule(ctx context.Context, project, name string) error {
	op, err := f.client.Delete(ctx, &computepb.DeleteFirewallRequest{
		Project:  project,
		Firewall: name,
	})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting firewall rule %s: %w", name, err)
	}
	return op.Wait(ctx)
}

func (f *FirewallManager) insertFirewall(ctx context.Context, p FirewallParams) error {
	op, err := f.client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: p.Project,
		FirewallResource: &computepb.Firewall{
			Name:              proto.String(p.Name),
			Network:           proto.String(p.Network),
			Direction:         proto.String("INGRESS"),
			Priority:          proto.Int32(1000),
			SourceRanges:      p.SourceRanges,
			DestinationRanges: p.DestRanges,
			Allowed: []*computepb.Allowed{{
				IPProtocol: proto.String("tcp"),
			}},
			Description: proto.String("Auto-managed by gke-vpc-fwr-rules-controller"),
		},
	})
	if err != nil {
		if isConflict(err) {
			return nil
		}
		return fmt.Errorf("inserting firewall rule %s: %w", p.Name, err)
	}
	return op.Wait(ctx)
}

func (f *FirewallManager) patchFirewall(ctx context.Context, p FirewallParams) error {
	op, err := f.client.Patch(ctx, &computepb.PatchFirewallRequest{
		Project:  p.Project,
		Firewall: p.Name,
		FirewallResource: &computepb.Firewall{
			SourceRanges:      p.SourceRanges,
			DestinationRanges: p.DestRanges,
			Allowed: []*computepb.Allowed{{
				IPProtocol: proto.String("tcp"),
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("patching firewall rule %s: %w", p.Name, err)
	}
	return op.Wait(ctx)
}

// needsUpdate checks if the existing firewall rule differs from the desired state.
func needsUpdate(existing *computepb.Firewall, desired FirewallParams) bool {
	if !stringSlicesEqual(existing.GetSourceRanges(), desired.SourceRanges) {
		return true
	}
	if !stringSlicesEqual(existing.GetDestinationRanges(), desired.DestRanges) {
		return true
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSet := make(map[string]struct{}, len(a))
	for _, s := range a {
		aSet[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := aSet[s]; !ok {
			return false
		}
	}
	return true
}

// FirewallRuleName generates a deterministic firewall rule name for a cluster.
func FirewallRuleName(clusterName string) string {
	base := fmt.Sprintf("gke-%s-gw-proxy-to-pods", clusterName)
	if len(base) <= 63 {
		return base
	}
	h := sha256.Sum256([]byte(clusterName))
	return fmt.Sprintf("gke-%.40s-%x-gw-proxy", clusterName, h[:4])
}

func isNotFound(err error) bool {
	if apiErr, ok := err.(*googleapi.Error); ok {
		return apiErr.Code == 404
	}
	return strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found")
}

func isConflict(err error) bool {
	if apiErr, ok := err.(*googleapi.Error); ok {
		return apiErr.Code == 409
	}
	return false
}

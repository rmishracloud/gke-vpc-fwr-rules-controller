package gcp

import (
	"errors"
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
)

func TestFirewallRuleName(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		wantExact   string
		wantPrefix  string
		wantMaxLen  int
	}{
		{
			name:        "short cluster name",
			clusterName: "prod-cluster",
			wantExact:   "gke-prod-cluster-gw-proxy-to-pods",
		},
		{
			name:        "single char",
			clusterName: "a",
			wantExact:   "gke-a-gw-proxy-to-pods",
		},
		{
			name:        "exactly 63 chars without truncation",
			clusterName: strings.Repeat("a", 63-len("gke--gw-proxy-to-pods")),
			wantPrefix:  "gke-",
			wantMaxLen:  63,
		},
		{
			name:        "long cluster name triggers truncation",
			clusterName: strings.Repeat("x", 80),
			wantPrefix:  "gke-",
			wantMaxLen:  63,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FirewallRuleName(tt.clusterName)

			if tt.wantExact != "" && got != tt.wantExact {
				t.Errorf("FirewallRuleName(%q) = %q, want %q", tt.clusterName, got, tt.wantExact)
			}
			if tt.wantPrefix != "" && !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("FirewallRuleName(%q) = %q, want prefix %q", tt.clusterName, got, tt.wantPrefix)
			}
			if tt.wantMaxLen > 0 && len(got) > tt.wantMaxLen {
				t.Errorf("FirewallRuleName(%q) = %q, length %d exceeds max %d", tt.clusterName, got, len(got), tt.wantMaxLen)
			}
		})
	}
}

func TestFirewallRuleNameDeterministic(t *testing.T) {
	cluster := strings.Repeat("long", 20)
	first := FirewallRuleName(cluster)
	second := FirewallRuleName(cluster)
	if first != second {
		t.Errorf("FirewallRuleName not deterministic: %q vs %q", first, second)
	}
}

func TestNeedsUpdate(t *testing.T) {
	tests := []struct {
		name     string
		existing *computepb.Firewall
		desired  FirewallParams
		want     bool
	}{
		{
			name: "identical — no update",
			existing: &computepb.Firewall{
				SourceRanges:      []string{"10.0.0.0/24"},
				DestinationRanges: []string{"192.168.0.0/16"},
			},
			desired: FirewallParams{
				SourceRanges: []string{"10.0.0.0/24"},
				DestRanges:   []string{"192.168.0.0/16"},
			},
			want: false,
		},
		{
			name: "source range changed",
			existing: &computepb.Firewall{
				SourceRanges:      []string{"10.0.0.0/24"},
				DestinationRanges: []string{"192.168.0.0/16"},
			},
			desired: FirewallParams{
				SourceRanges: []string{"10.0.1.0/24"},
				DestRanges:   []string{"192.168.0.0/16"},
			},
			want: true,
		},
		{
			name: "destination range changed",
			existing: &computepb.Firewall{
				SourceRanges:      []string{"10.0.0.0/24"},
				DestinationRanges: []string{"192.168.0.0/16"},
			},
			desired: FirewallParams{
				SourceRanges: []string{"10.0.0.0/24"},
				DestRanges:   []string{"10.200.0.0/16"},
			},
			want: true,
		},
		{
			name: "order differs — no update (set equality)",
			existing: &computepb.Firewall{
				SourceRanges:      []string{"35.191.0.0/16", "130.211.0.0/22"},
				DestinationRanges: []string{"192.168.0.0/16"},
			},
			desired: FirewallParams{
				SourceRanges: []string{"130.211.0.0/22", "35.191.0.0/16"},
				DestRanges:   []string{"192.168.0.0/16"},
			},
			want: false,
		},
		{
			name: "additional source range",
			existing: &computepb.Firewall{
				SourceRanges:      []string{"10.0.0.0/24"},
				DestinationRanges: []string{"192.168.0.0/16"},
			},
			desired: FirewallParams{
				SourceRanges: []string{"10.0.0.0/24", "10.0.1.0/24"},
				DestRanges:   []string{"192.168.0.0/16"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsUpdate(tt.existing, tt.desired)
			if got != tt.want {
				t.Errorf("needsUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStringSlicesEqual(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{"both empty", []string{}, []string{}, true},
		{"both nil", nil, nil, true},
		{"one nil one empty", nil, []string{}, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different elements", []string{"a", "b"}, []string{"a", "c"}, false},
		{"duplicate in one only", []string{"a", "a"}, []string{"a", "b"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSlicesEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("stringSlicesEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"googleapi 404", &googleapi.Error{Code: 404}, true},
		{"googleapi 403", &googleapi.Error{Code: 403}, false},
		{"googleapi 500", &googleapi.Error{Code: 500}, false},
		{"error string contains 404", errors.New("request failed: 404 not found"), true},
		{"error string contains 'not found'", errors.New("resource not found"), true},
		{"unrelated error", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFound(tt.err)
			if got != tt.want {
				t.Errorf("isNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"googleapi 409", &googleapi.Error{Code: 409}, true},
		{"googleapi 404", &googleapi.Error{Code: 404}, false},
		{"plain error with 409 in message", errors.New("status 409 conflict"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConflict(tt.err)
			if got != tt.want {
				t.Errorf("isConflict(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

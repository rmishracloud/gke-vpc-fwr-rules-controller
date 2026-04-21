package gcp

import "testing"

func TestParseProjectFromSelfLink(t *testing.T) {
	tests := []struct {
		name     string
		selfLink string
		want     string
		wantErr  bool
	}{
		{
			name:     "short form",
			selfLink: "projects/my-host-proj/global/networks/shared-vpc",
			want:     "my-host-proj",
		},
		{
			name:     "full URL",
			selfLink: "https://www.googleapis.com/compute/v1/projects/my-host-proj/global/networks/shared-vpc",
			want:     "my-host-proj",
		},
		{
			name:     "subnet self-link",
			selfLink: "projects/my-host-proj/regions/us-central1/subnetworks/proxy-only",
			want:     "my-host-proj",
		},
		{
			name:     "no projects segment",
			selfLink: "invalid/path",
			wantErr:  true,
		},
		{
			name:     "empty string",
			selfLink: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProjectFromSelfLink(tt.selfLink)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseProjectFromSelfLink() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseProjectFromSelfLink(%q) = %q, want %q", tt.selfLink, got, tt.want)
			}
		})
	}
}

func TestNormalizeNetworkSelfLink(t *testing.T) {
	tests := []struct {
		name     string
		selfLink string
		want     string
	}{
		{
			name:     "full URL",
			selfLink: "https://www.googleapis.com/compute/v1/projects/my-host-proj/global/networks/shared-vpc",
			want:     "projects/my-host-proj/global/networks/shared-vpc",
		},
		{
			name:     "short form unchanged",
			selfLink: "projects/my-host-proj/global/networks/shared-vpc",
			want:     "projects/my-host-proj/global/networks/shared-vpc",
		},
		{
			name:     "no projects segment returned as-is",
			selfLink: "some/other/path",
			want:     "some/other/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeNetworkSelfLink(tt.selfLink)
			if got != tt.want {
				t.Errorf("normalizeNetworkSelfLink(%q) = %q, want %q", tt.selfLink, got, tt.want)
			}
		})
	}
}

func TestNetworkSelfLinksMatch(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "full URL vs short form — same network",
			a:    "https://www.googleapis.com/compute/v1/projects/my-host-proj/global/networks/shared-vpc",
			b:    "projects/my-host-proj/global/networks/shared-vpc",
			want: true,
		},
		{
			name: "both full URLs — same network",
			a:    "https://www.googleapis.com/compute/v1/projects/my-host-proj/global/networks/shared-vpc",
			b:    "https://www.googleapis.com/compute/v1/projects/my-host-proj/global/networks/shared-vpc",
			want: true,
		},
		{
			name: "both short form — same network",
			a:    "projects/my-host-proj/global/networks/shared-vpc",
			b:    "projects/my-host-proj/global/networks/shared-vpc",
			want: true,
		},
		{
			name: "different projects",
			a:    "projects/host-a/global/networks/shared-vpc",
			b:    "projects/host-b/global/networks/shared-vpc",
			want: false,
		},
		{
			name: "different network names",
			a:    "projects/my-host-proj/global/networks/vpc-a",
			b:    "projects/my-host-proj/global/networks/vpc-b",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := networkSelfLinksMatch(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("networkSelfLinksMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

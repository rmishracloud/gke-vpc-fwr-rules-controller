package gcp

import "testing"

func TestRegionFromZone(t *testing.T) {
	tests := []struct {
		name    string
		zone    string
		want    string
		wantErr bool
	}{
		{"us-central1-a", "us-central1-a", "us-central1", false},
		{"europe-west4-b", "europe-west4-b", "europe-west4", false},
		{"asia-northeast1-c", "asia-northeast1-c", "asia-northeast1", false},
		{"no dash — invalid", "invalid", "", true},
		{"empty string", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RegionFromZone(tt.zone)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RegionFromZone(%q) err = %v, wantErr %v", tt.zone, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("RegionFromZone(%q) = %q, want %q", tt.zone, got, tt.want)
			}
		})
	}
}

package allocator

import (
	"net"
	"testing"
)

func TestIpToUint32(t *testing.T) {
	tests := []struct {
		name    string
		ip      net.IP
		want    uint32
		wantErr bool
	}{
		{
			name:    "zero ip",
			ip:      net.ParseIP("0.0.0.0"),
			want:    0x00000000,
			wantErr: false,
		},
		{
			name:    "max ip",
			ip:      net.ParseIP("255.255.255.255"),
			want:    0xFFFFFFFF,
			wantErr: false,
		},
		{
			name:    "localhost",
			ip:      net.ParseIP("127.0.0.1"),
			want:    0x7F000001,
			wantErr: false,
		},
		{
			name:    "common gateway",
			ip:      net.ParseIP("192.168.1.1"),
			want:    0xC0A80101,
			wantErr: false,
		},
		{
			name:    "8.8.8.8",
			ip:      net.ParseIP("8.8.8.8"),
			want:    0x08080808,
			wantErr: false,
		},
		{
			name:    "10.0.0.1",
			ip:      net.ParseIP("10.0.0.1"),
			want:    0x0A000001,
			wantErr: false,
		},
		{
			name:    "172.16.0.1",
			ip:      net.ParseIP("172.16.0.1"),
			want:    0xAC100001,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ipToUint32(tt.ip)
			if got != tt.want {
				t.Errorf("ipToUint32() = 0x%08X, want 0x%08X", got, tt.want)
			}
		})
	}
}

func TestIpToUint32Panic(t *testing.T) {
	tests := []struct {
		name string
		ip   net.IP
	}{
		{
			name: "ipv6",
			ip:   net.ParseIP("2001:db8::1"),
		},
		{
			name: "nil ip",
			ip:   net.IP(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("ipToUint32() expected panic, got none")
				}
			}()
			ipToUint32(tt.ip)
		})
	}
}

func TestUint32ToIp(t *testing.T) {
	tests := []struct {
		name string
		n    uint32
		want net.IP
	}{
		{
			name: "zero",
			n:    0x00000000,
			want: net.ParseIP("0.0.0.0"),
		},
		{
			name: "max uint32",
			n:    0xFFFFFFFF,
			want: net.ParseIP("255.255.255.255"),
		},
		{
			name: "localhost",
			n:    0x7F000001,
			want: net.ParseIP("127.0.0.1"),
		},
		{
			name: "192.168.1.1",
			n:    0xC0A80101,
			want: net.ParseIP("192.168.1.1"),
		},
		{
			name: "8.8.8.8",
			n:    0x08080808,
			want: net.ParseIP("8.8.8.8"),
		},
		{
			name: "10.0.0.1",
			n:    0x0A000001,
			want: net.ParseIP("10.0.0.1"),
		},
		{
			name: "172.16.0.1",
			n:    0xAC100001,
			want: net.ParseIP("172.16.0.1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uint32ToIp(tt.n)
			if !got.Equal(tt.want) {
				t.Errorf("uint32ToIp() = %s, want %s", got.String(), tt.want.String())
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{
			name: "zero",
			ip:   "0.0.0.0",
		},
		{
			name: "max",
			ip:   "255.255.255.255",
		},
		{
			name: "localhost",
			ip:   "127.0.0.1",
		},
		{
			name: "common gateway",
			ip:   "192.168.1.1",
		},
		{
			name: "google dns",
			ip:   "8.8.8.8",
		},
		{
			name: "private range",
			ip:   "10.20.30.40",
		},
		{
			name: "broadcast",
			ip:   "192.168.255.255",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := net.ParseIP(tt.ip)
			converted := ipToUint32(original)
			roundTrip := uint32ToIp(converted)

			if !roundTrip.Equal(original) {
				t.Errorf("round trip failed: %s -> 0x%08X -> %s", original.String(), converted, roundTrip.String())
			}
		})
	}
}

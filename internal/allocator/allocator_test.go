package allocator

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAllocatorIPv4(t *testing.T) {
	tests := []struct {
		name       string
		cidr       string
		wantBase   uint32
		wantWords  int
		wantErr    bool
	}{
		{
			name:      "small network /30",
			cidr:      "192.168.1.0/30",
			wantBase:  ipToUint32(net.ParseIP("192.168.1.0")),
			wantWords: 1, // 4 hosts = 4 bits = 1 word
			wantErr:   false,
		},
		{
			name:      "medium network /24",
			cidr:      "10.0.0.0/24",
			wantBase:  ipToUint32(net.ParseIP("10.0.0.0")),
			wantWords: 4, // 256 hosts = 256 bits = 4 words
			wantErr:   false,
		},
		{
			name:      "large network /16",
			cidr:      "172.16.0.0/16",
			wantBase:  ipToUint32(net.ParseIP("172.16.0.0")),
			wantWords: 1024, // 65536 hosts = 65536 bits = 1024 words
			wantErr:   false,
		},
		{
			name:    "invalid CIDR",
			cidr:    "not a cidr",
			wantErr: true,
		},
		{
			name:    "IPv6 not supported",
			cidr:    "2001:db8::/32",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(tt.cidr)
			if tt.wantErr {
				if err == nil {
					t.Error("New() expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			defer a.Stop()
			if a.base != tt.wantBase {
				t.Errorf("base = 0x%08X, want 0x%08X", a.base, tt.wantBase)
			}
			if len(a.bitmap) != tt.wantWords {
				t.Errorf("bitmap len = %d, want %d", len(a.bitmap), tt.wantWords)
			}
		})
	}
}

func TestAllocateBasic(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	leases := make([]*Lease, 4)
	for i := 0; i < 4; i++ {
		lease, err := a.Allocate()
		if err != nil {
			t.Fatalf("Allocate() iter %d error: %v", i, err)
		}
		if lease == nil {
			t.Fatalf("Allocate() returned nil lease at iter %d", i)
		}
		leases[i] = lease
	}

	_, err = a.Allocate()
	if err != ErrFullyAllocated {
		t.Errorf("Allocate() on full pool expected ErrFullyAllocated, got %v", err)
	}

	ipMap := make(map[uint32]bool)
	for _, lease := range leases {
		if ipMap[lease.IP] {
			t.Errorf("duplicate IP allocated: 0x%08X", lease.IP)
		}
		ipMap[lease.IP] = true
	}
}

func TestAllocateIPCorrectness(t *testing.T) {
	cidr := "10.0.0.0/24"
	a, err := New(cidr)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	_, ipNet, _ := net.ParseCIDR(cidr)
	expectedBase := ipToUint32(ipNet.IP)
	ones, bits := ipNet.Mask.Size()
	maxHosts := 1 << uint(bits-ones)

	for i := 0; i < maxHosts; i++ {
		lease, err := a.Allocate()
		if err != nil {
			t.Fatalf("Allocate() iter %d error: %v", i, err)
		}
		if lease.IP < expectedBase || lease.IP >= expectedBase+uint32(maxHosts) {
			t.Errorf("allocated IP 0x%08X out of range [0x%08X, 0x%08X)",
				lease.IP, expectedBase, expectedBase+uint32(maxHosts))
		}
	}
}

func TestRenewExtendsTTL(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	lease, err := a.Allocate()
	if err != nil {
		t.Fatalf("Allocate() error: %v", err)
	}

	originalExpiry := lease.ExpiresAt
	originalLastSeen := lease.LastSeen

	time.Sleep(100 * time.Millisecond)

	err = a.Renew(Uint32ToIp(lease.IP))
	if err != nil {
		t.Fatalf("Renew() error: %v", err)
	}

	a.mu.Lock()
	updatedLease := a.leases[lease.IP]
	a.mu.Unlock()

	if !updatedLease.ExpiresAt.After(originalExpiry) {
		t.Errorf("Renew() did not extend expiry time")
	}
	if !updatedLease.LastSeen.After(originalLastSeen) {
		t.Errorf("Renew() did not update LastSeen")
	}
}

func TestRenewNonexistentLease(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	fakeLease := &Lease{IP: ipToUint32(net.ParseIP("192.168.1.1"))}
	err = a.Renew(Uint32ToIp(fakeLease.IP))
	if err != ErrUnAcquiredLease {
		t.Errorf("Renew(nonexistent) expected ErrUnAcquiredLease, got %v", err)
	}
}

func TestReleaseBitmapReset(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	// Allocate and verify bit is set
	lease, err := a.Allocate()
	if err != nil {
		t.Fatalf("Allocate() error: %v", err)
	}

	idx := lease.IP - a.base
	w := idx >> 6
	b := idx & 63

	a.mu.Lock()
	bitSetBefore := (a.bitmap[w] & (1 << b)) != 0
	a.mu.Unlock()

	if !bitSetBefore {
		t.Error("bit not set after allocation")
	}

	// Release and verify bit is cleared
	err = a.Release(Uint32ToIp(lease.IP))
	if err != nil {
		t.Fatalf("Release() error: %v", err)
	}

	a.mu.Lock()
	bitSetAfter := (a.bitmap[w] & (1 << b)) != 0
	a.mu.Unlock()

	if bitSetAfter {
		t.Error("bit not cleared after release")
	}
}

func TestReleaseRemovesLease(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	lease, err := a.Allocate()
	if err != nil {
		t.Fatalf("Allocate() error: %v", err)
	}

	a.mu.Lock()
	_, exists := a.leases[lease.IP]
	a.mu.Unlock()

	if !exists {
		t.Error("lease not in map after allocation")
	}

	err = a.Release(Uint32ToIp(lease.IP))
	if err != nil {
		t.Fatalf("Release() error: %v", err)
	}

	a.mu.Lock()
	_, exists = a.leases[lease.IP]
	a.mu.Unlock()

	if exists {
		t.Error("lease still in map after release")
	}
}

func TestReleaseNonexistentLease(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	fakeLease := &Lease{IP: ipToUint32(net.ParseIP("192.168.1.1"))}
	err = a.Release(Uint32ToIp(fakeLease.IP))
	if err != ErrUnAcquiredLease {
		t.Errorf("Release(nonexistent) expected ErrUnAcquiredLease, got %v", err)
	}
}

func TestReleaseRealloc(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	lease1, err := a.Allocate()
	if err != nil {
		t.Fatalf("first Allocate() error: %v", err)
	}
	ip1 := lease1.IP

	err = a.Release(Uint32ToIp(lease1.IP))
	if err != nil {
		t.Fatalf("Release() error: %v", err)
	}

	lease2, err := a.Allocate()
	if err != nil {
		t.Fatalf("second Allocate() error: %v", err)
	}

	if lease2.IP != ip1 {
		t.Errorf("reallocated IP 0x%08X, want 0x%08X", lease2.IP, ip1)
	}
}

func TestConcurrentAllocate(t *testing.T) {
	a, err := New("10.0.0.0/24")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	numGoroutines := 50
	allocsPerGoroutine := 5
	results := make(chan *Lease, numGoroutines*allocsPerGoroutine)
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < allocsPerGoroutine; i++ {
				lease, err := a.Allocate()
				if err == nil {
					results <- lease
				}
			}
		}()
	}

	wg.Wait()
	close(results)

	ipMap := make(map[uint32]bool)
	count := 0
	for lease := range results {
		if ipMap[lease.IP] {
			t.Errorf("concurrent allocate: duplicate IP 0x%08X", lease.IP)
		}
		ipMap[lease.IP] = true
		count++
	}

	if count != numGoroutines*allocsPerGoroutine {
		t.Logf("concurrent allocate: allocated %d IPs (may be less if pool full)", count)
	}
}

func TestConcurrentMixed(t *testing.T) {
	a, err := New("10.0.0.0/25")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	numGoroutines := 20
	opsPerGoroutine := 10
	var wg sync.WaitGroup
	var successCount int32

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				lease, err := a.Allocate()
				if err != nil {
					return
				}

				time.Sleep(time.Duration(i%3) * time.Millisecond)

				if i%2 == 0 {
					a.Renew(Uint32ToIp(lease.IP))
				}

				if err := a.Release(Uint32ToIp(lease.IP)); err == nil {
					atomic.AddInt32(&successCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	if successCount == 0 {
		t.Error("no operations succeeded under concurrent access")
	}
}

func TestReaperCleansExpired(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	a.defaultTTL = 100 * time.Millisecond

	lease, err := a.Allocate()
	if err != nil {
		t.Fatalf("Allocate() error: %v", err)
	}

	a.mu.Lock()
	initialCount := len(a.leases)
	a.mu.Unlock()

	if initialCount != 1 {
		t.Errorf("initial lease count = %d, want 1", initialCount)
	}

	time.Sleep(200 * time.Millisecond)
	a.reapExpired()

	a.mu.Lock()
	finalCount := len(a.leases)
	a.mu.Unlock()

	if finalCount != 0 {
		t.Errorf("lease not reaped after expiry, count = %d, want 0", finalCount)
	}

	newLease, err := a.Allocate()
	if err != nil {
		t.Fatalf("reallocate after reap failed: %v", err)
	}

	if newLease.IP != lease.IP {
		t.Logf("note: reallocated different IP (both were available)")
	}
}

func TestReaperDoesNotCleanActive(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	lease, err := a.Allocate()
	if err != nil {
		t.Fatalf("Allocate() error: %v", err)
	}

	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		a.Renew(Uint32ToIp(lease.IP))
	}

	a.reapExpired()

	a.mu.Lock()
	count := len(a.leases)
	a.mu.Unlock()

	if count != 1 {
		t.Errorf("active lease was reaped, count = %d, want 1", count)
	}
}

func TestEdgeCaseMultipleBitsInWord(t *testing.T) {
	// /27 = 32 hosts = 32 bits = < 1 word, fills first word
	a, err := New("192.168.1.0/27")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	leases := make([]*Lease, 32)
	for i := 0; i < 32; i++ {
		lease, err := a.Allocate()
		if err != nil {
			t.Fatalf("Allocate() iter %d error: %v", i, err)
		}
		leases[i] = lease
	}

	_, err = a.Allocate()
	if err != ErrFullyAllocated {
		t.Errorf("expected fully allocated, got %v", err)
	}

	for i := 0; i < 16; i++ {
		a.Release(Uint32ToIp(leases[i].IP))
	}

	for i := 0; i < 16; i++ {
		_, err := a.Allocate()
		if err != nil {
			t.Fatalf("reallocate iter %d error: %v", i, err)
		}
	}
}

func TestAllocationOrder(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	expectedIPs := []uint32{
		ipToUint32(net.ParseIP("192.168.1.0")),
		ipToUint32(net.ParseIP("192.168.1.1")),
		ipToUint32(net.ParseIP("192.168.1.2")),
		ipToUint32(net.ParseIP("192.168.1.3")),
	}

	for i, expectedIP := range expectedIPs {
		lease, err := a.Allocate()
		if err != nil {
			t.Fatalf("Allocate() iter %d error: %v", i, err)
		}
		if lease.IP != expectedIP {
			t.Errorf("allocation %d got 0x%08X, want 0x%08X", i, lease.IP, expectedIP)
		}
	}
}

func TestLeaseTimestamps(t *testing.T) {
	a, err := New("192.168.1.0/30")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer a.Stop()

	before := time.Now()
	lease, err := a.Allocate()
	after := time.Now()

	if err != nil {
		t.Fatalf("Allocate() error: %v", err)
	}

	if lease.LastSeen.Before(before) || lease.LastSeen.After(after.Add(100*time.Millisecond)) {
		t.Errorf("LastSeen not in expected range")
	}

	expectedExpiry := after.Add(a.defaultTTL)
	if lease.ExpiresAt.Before(expectedExpiry.Add(-100*time.Millisecond)) ||
		lease.ExpiresAt.After(expectedExpiry.Add(100*time.Millisecond)) {
		t.Errorf("ExpiresAt not in expected range")
	}
}

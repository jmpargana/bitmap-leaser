package allocator

import (
	"errors"
	"math/bits"
	"net"
	"sync"
	"time"
)

const (
	DEFAULT_TTL = 10 * time.Minute
	DEFAULT_CLEAN_UP = 1 * time.Second
)

var (
	ErrFullyAllocated = errors.New("fully allocated")
	ErrUnAcquiredLease = errors.New("lease you want to extend was never acquired")
	ErrIPv6NotSupported = errors.New("only IPv4 CIDR blocks supported")
)

type Allocator struct {
	base uint32
	maxIdx uint32 // max host index (size - 1)
	bitmap []uint64
	leases map[uint32]*Lease
	defaultTTL time.Duration
	cleanUp time.Duration
	done chan struct{}
	
	mu sync.Mutex
}

func New(prefix string) (*Allocator, error) {
	ip, ipNet, err := net.ParseCIDR(prefix)
	if err != nil {
		return nil, err
	}
	
	if ip.To4() == nil {
		return nil, ErrIPv6NotSupported
	}
	
	ones, bits := ipNet.Mask.Size()
	hostBits := bits - ones
	size := (1 << hostBits)
	words := (size + 63) >> 6

	a := &Allocator{
		bitmap: make([]uint64, words),
		leases: make(map[uint32]*Lease),
		defaultTTL: DEFAULT_TTL,
		cleanUp: DEFAULT_CLEAN_UP,
		base: IpToUint32(ipNet.IP),
		maxIdx: uint32(size - 1),
		done: make(chan struct{}),
	}
	go a.startReaper()
	return a, nil
}

func (a *Allocator) Allocate() (*Lease, error) {
	a.mu.Lock()	
	defer a.mu.Unlock()

	for i, word := range a.bitmap {
		free := ^word
		if free == 0 { // opposite of all allocated is all 0
			continue
		}
		
		bit := bits.TrailingZeros64(free) // first non zero (original 0)
		idx := (i << 6) + bit // multiply by word index
		
		if uint32(idx) > a.maxIdx {
			return nil, ErrFullyAllocated
		}
		
		a.bitmap[i] |= 1 << bit // replace position with one

		ip := a.base + uint32(idx)

		now := time.Now()
		lease := &Lease{
			IP: ip,
			ExpiresAt: now.Add(a.defaultTTL),
			LastSeen: now,
		}
		a.leases[ip] = lease

		return lease, nil
	}
	return nil, ErrFullyAllocated
}

func (a *Allocator) Renew(ip net.IP) error {
	uip := ipToUint32(ip)
	a.mu.Lock()	
	defer a.mu.Unlock()
	
	lease, ok := a.leases[uip]
	if !ok {
		return ErrUnAcquiredLease
	}
	
	now := time.Now()
	lease.ExpiresAt = now.Add(a.defaultTTL)
	lease.LastSeen = now
	
	return nil
}

func (a *Allocator) Release(ip net.IP) error {
	uip := ipToUint32(ip)
	a.mu.Lock()
	defer a.mu.Unlock()
	
	_, ok := a.leases[uip]
	if !ok {
		return ErrUnAcquiredLease
	}
	
	return a.releaseLocked(&Lease{IP: uip})
}

func (a *Allocator) startReaper() {
	ticker := time.NewTicker(a.cleanUp)
	for {
		select {
		case <-ticker.C:
			a.reapExpired()
		case <-a.done:
			ticker.Stop()
			return
		}
	}
}

func (a *Allocator) Stop() {
	close(a.done)
}

func (a *Allocator) reapExpired() {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	now := time.Now()
	var toDelete []uint32
	
	for ip, lease := range a.leases {
		if lease.ExpiresAt.Before(now) {
			toDelete = append(toDelete, ip)
		}
	}
	
	for _, ip := range toDelete {
		a.releaseLocked(&Lease{IP: ip})
	}
}

func (a *Allocator) releaseLocked(l *Lease) error {
	delete(a.leases, l.IP)
	
	idx := l.IP - a.base
	w := idx >> 6
	b := idx & 63
	
	a.bitmap[w] &^= 1 << b
	
	return nil
}
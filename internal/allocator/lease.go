package allocator

import "time"

type Lease struct {
	IP        uint32    `json:"ip,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
}

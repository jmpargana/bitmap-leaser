package allocator

import (
	"encoding/binary"
	"net"
)

func IpToUint32(ip net.IP) uint32 {
	return ipToUint32(ip)
}

func Uint32ToIp(n uint32) net.IP {
	return uint32ToIp(n)
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()	
	if ip == nil {
		panic("only works for ipv4")
	}
	return binary.BigEndian.Uint32(ip)
}

func uint32ToIp(n uint32) net.IP {
	ip := make([]byte, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}
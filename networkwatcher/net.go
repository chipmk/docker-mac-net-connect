//go:build darwin

package networkwatcher

import "net"

// isIPv4CIDR returns true if the CIDR is IPv4. We only add IPv4 routes
// since the macOS route command uses -inet.
func isIPv4CIDR(cidr string) bool {
	ip, _, err := net.ParseCIDR(cidr)
	return err == nil && ip.To4() != nil
}

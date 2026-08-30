package netprobe

import (
	"fmt"
	"net"
)

// A /24 covers the overwhelming majority of home networks.
// The watcher sits in the same network as the server, so the mask is right
// here rather than guessed. Assuming /24 breaks silently on a /16 or /22, and
// the only symptom is that waking never works.
func GuessBroadcast(serverIP string) string {
	ip := net.ParseIP(serverIP).To4()
	if ip == nil {
		return "255.255.255.255"
	}
	if found := BroadcastForIP(ip, LocalNetworks()); found != "" {
		return found
	}
	return fmt.Sprintf("%d.%d.%d.255", ip[0], ip[1], ip[2])
}

func LocalNetworks() []*net.IPNet {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	nets := []*net.IPNet{}
	for _, addr := range addrs {
		if network, ok := addr.(*net.IPNet); ok && network.IP.To4() != nil {
			nets = append(nets, network)
		}
	}
	return nets
}

// Host bits all set, which is the broadcast address of that subnet.
func BroadcastForIP(ip net.IP, nets []*net.IPNet) string {
	for _, network := range nets {
		if !network.Contains(ip) {
			continue
		}
		mask := network.Mask
		if len(mask) == net.IPv6len {
			mask = mask[12:]
		}
		if len(mask) != net.IPv4len {
			continue
		}
		base := network.IP.To4()
		broadcast := make(net.IP, net.IPv4len)
		for i := range broadcast {
			broadcast[i] = base[i] | ^mask[i]
		}
		return broadcast.String()
	}
	return ""
}

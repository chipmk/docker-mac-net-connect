//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

func main() {
	interfaceName := "chip0"

	serverPortString := os.Getenv("SERVER_PORT")
	if serverPortString == "" {
		fmt.Printf("SERVER_PORT is not set\n")
		os.Exit(ExitSetupFailed)
	}

	serverPort, err := strconv.Atoi(serverPortString)
	if err != nil {
		fmt.Printf("SERVER_PORT is not an integer\n")
		os.Exit(ExitSetupFailed)
	}

	hostPeerIp := os.Getenv("HOST_PEER_IP")
	if hostPeerIp == "" {
		fmt.Printf("HOST_PEER_IP is not set\n")
		os.Exit(ExitSetupFailed)
	}

	vmPeerIp := os.Getenv("VM_PEER_IP")
	if vmPeerIp == "" {
		fmt.Printf("VM_PEER_IP is not set\n")
		os.Exit(ExitSetupFailed)
	}

	hostPublicKeyString := os.Getenv("HOST_PUBLIC_KEY")
	if hostPublicKeyString == "" {
		fmt.Printf("HOST_PUBLIC_KEY is not set\n")
		os.Exit(ExitSetupFailed)
	}

	vmPrivateKeyString := os.Getenv("VM_PRIVATE_KEY")
	if vmPrivateKeyString == "" {
		fmt.Printf("VM_PRIVATE_KEY is not set\n")
		os.Exit(ExitSetupFailed)
	}

	links, err := netlink.LinkList()
	if err != nil {
		fmt.Printf("Could not list links: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	for _, link := range links {
		if link.Attrs().Name == interfaceName {
			fmt.Printf("Interface %s already exists. Removing.\n", interfaceName)

			err = netlink.LinkDel(link)
			if err != nil {
				fmt.Printf("Could not delete link %s: %v\n", interfaceName, err)
				os.Exit(ExitSetupFailed)
			}
		}
	}

	linkAttrs := netlink.NewLinkAttrs()
	linkAttrs.Name = interfaceName

	fmt.Printf("Creating WireGuard interface %s\n", interfaceName)

	wireguard := &netlink.Wireguard{LinkAttrs: linkAttrs}
	err = netlink.LinkAdd(wireguard)
	if err != nil {
		fmt.Printf("Could not add link %s: %v\n", linkAttrs.Name, err)
	}

	vmIpNet, err := netlink.ParseIPNet(vmPeerIp + "/32")
	if err != nil {
		fmt.Printf("Could not parse VM peer IPNet: %v\n", err)
	}
	hostIpNet, err := netlink.ParseIPNet(hostPeerIp + "/32")
	if err != nil {
		fmt.Printf("Could not parse host peer IPNet: %v\n", err)
	}

	fmt.Println("Assigning IP to WireGuard interface")

	addr := netlink.Addr{IPNet: vmIpNet, Peer: hostIpNet}
	err = netlink.AddrAdd(wireguard, &addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not add address %v to WireGuard interface: %v\n", addr, err)
	}

	c, err := wgctrl.New()
	if err != nil {
		fmt.Printf("Failed to create wgctrl client: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	defer func() { _ = c.Close() }()

	vmPrivateKey, err := wgtypes.ParseKey(vmPrivateKeyString)
	if err != nil {
		fmt.Printf("Failed to parse VM private key: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	hostPublicKey, err := wgtypes.ParseKey(hostPublicKeyString)
	if err != nil {
		fmt.Printf("Failed to parse host public key: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	wildcardIpNet, err := netlink.ParseIPNet("0.0.0.0/0")
	if err != nil {
		fmt.Printf("Failed to parse wildcard IPNet: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	ips, err := net.LookupIP("host.docker.internal")
	if err != nil || len(ips) == 0 {
		fmt.Printf("Failed to lookup IP: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	persistentKeepaliveInterval, err := time.ParseDuration("25s")
	if err != nil {
		fmt.Printf("Failed to parse duration: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	peer := wgtypes.PeerConfig{
		PublicKey:                   hostPublicKey,
		Endpoint:                    &net.UDPAddr{IP: ips[0], Port: serverPort},
		PersistentKeepaliveInterval: &persistentKeepaliveInterval,
		AllowedIPs: []net.IPNet{
			*wildcardIpNet,
			*hostIpNet,
		},
	}

	fmt.Println("Configuring WireGuard device")

	err = c.ConfigureDevice(interfaceName, wgtypes.Config{
		PrivateKey: &vmPrivateKey,
		Peers:      []wgtypes.PeerConfig{peer},
	})
	if err != nil {
		fmt.Printf("Failed to configure wireguard device: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	err = netlink.LinkSetUp(wireguard)
	if err != nil {
		fmt.Printf("Failed to set wireguard link to up: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	// Look up the WireGuard link for policy routing setup
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		fmt.Printf("Failed to get link %s: %v\n", interfaceName, err)
		os.Exit(ExitSetupFailed)
	}

	ipt, err := iptables.New()
	if err != nil {
		fmt.Printf("Failed to create new iptables client: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	fmt.Println("Adding iptables rules for WireGuard interface")

	// Allow WireGuard traffic through the raw table. Docker 28.0+ adds
	// "Direct Access Filtering" - per-container DROP rules in raw PREROUTING
	// that block traffic to container IPs from non-bridge interfaces. Since
	// raw is processed before mangle and filter, our DOCKER-USER rules never
	// see the packets without this.
	err = ipt.Insert(
		"raw", "PREROUTING", 1,
		"-i", interfaceName,
		"-j", "ACCEPT",
	)
	if err != nil {
		fmt.Printf("Failed to add raw accept rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	// Accept all traffic entering via WireGuard. DOCKER-USER is evaluated
	// before Docker's own DOCKER chain, bypassing its DROP rules for
	// traffic from non-bridge interfaces (added in Docker Desktop 4.39.0).
	err = ipt.AppendUnique(
		"filter", "DOCKER-USER",
		"-i", interfaceName,
		"-j", "ACCEPT",
	)
	if err != nil {
		fmt.Printf("Failed to add iptables filter rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}
	err = ipt.AppendUnique(
		"filter", "DOCKER-USER",
		"-o", interfaceName,
		"-j", "ACCEPT",
	)
	if err != nil {
		fmt.Printf("Failed to add iptables filter rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	// Masquerade traffic from the macOS host (identified by its WireGuard IP)
	// so containers on internal networks can reply. Internal networks have no
	// default gateway, so the source must be rewritten to the bridge IP.
	// Traffic from other sources (eg. LAN devices) is not masqueraded,
	// preserving original source IPs.
	err = ipt.AppendUnique(
		"nat", "POSTROUTING",
		"-s", hostPeerIp,
		"-j", "MASQUERADE",
	)
	if err != nil {
		fmt.Printf("Failed to add masquerade rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	// Tag conntrack entries for connections entering via WireGuard.
	// This allows reply packets to be routed back through the tunnel
	// without masquerading, preserving the original source IP.
	err = ipt.AppendUnique(
		"mangle", "PREROUTING",
		"-i", interfaceName,
		"-j", "CONNMARK", "--set-mark", "0x1",
	)
	if err != nil {
		fmt.Printf("Failed to add connmark rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	// Restore conntrack mark to packet mark on reply packets so
	// policy routing can send them back through WireGuard
	err = ipt.AppendUnique(
		"mangle", "PREROUTING",
		"!", "-i", interfaceName,
		"-m", "connmark", "--mark", "0x1",
		"-j", "CONNMARK", "--restore-mark",
	)
	if err != nil {
		fmt.Printf("Failed to add mark restore rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	fmt.Println("Adding policy routing for WireGuard return path")

	// Route marked packets back through WireGuard
	rule := netlink.NewRule()
	rule.Mark = 1
	mask := uint32(1)
	rule.Mask = &mask
	rule.Table = 100
	_ = netlink.RuleDel(rule) // remove if exists
	err = netlink.RuleAdd(rule)
	if err != nil {
		fmt.Printf("Failed to add routing rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	err = netlink.RouteReplace(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Table:     100,
		Dst:       &net.IPNet{IP: net.IPv4(0, 0, 0, 0), Mask: net.CIDRMask(0, 32)},
	})
	if err != nil {
		fmt.Printf("Failed to add route: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	// Report CNI routes from the VM so the host can add them for k8s pod
	// connectivity. Docker Desktop doesn't set spec.podCIDR on nodes, so
	// the host can't discover pod CIDRs via the k8s API alone.
	cni, err := netlink.LinkByName("cni0")
	if err == nil {
		routes, err := netlink.RouteList(cni, netlink.FAMILY_V4)
		if err == nil {
			for _, r := range routes {
				if r.Dst != nil {
					fmt.Printf("VM_ROUTE=%s\n", r.Dst.String())
				}
			}
		}
	}
}

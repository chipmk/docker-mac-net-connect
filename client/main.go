package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
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

func cleanup(interfaceName string, ipt *iptables.IPTables, hostPeerIp string, dockerCIDRs []string, bridgeIp string, bridgeInterface string) {
	if ipt != nil {
		fmt.Println("Removing iptables NAT rules")
		for _, cidr := range dockerCIDRs {
			fmt.Printf("Removing NAT rule for CIDR: %s\n", cidr)
			ipt.Delete("nat", "POSTROUTING", "-s", hostPeerIp, "-d", cidr, "-o", "docker+", "-j", "MASQUERADE")
		}

		fmt.Println("Removing iptables filter rules")
		for _, cidr := range dockerCIDRs {
			fmt.Printf("Removing filter rule for CIDR: %s\n", cidr)
			ipt.Delete("filter", "DOCKER", "-s", hostPeerIp, "-d", cidr, "-o", "docker+", "-j", "ACCEPT")
		}

		if bridgeIp != "" {
			fmt.Println("Removing bridge DOCKER-USER rules")
			for _, cidr := range dockerCIDRs {
				fmt.Printf("Removing DOCKER-USER rule for bridge IP %s to CIDR: %s\n", bridgeIp, cidr)
				ipt.Delete("filter", "DOCKER-USER", "-s", bridgeIp, "-d", cidr, "-i", bridgeInterface, "-o", "docker+", "-j", "ACCEPT")
			}
		}
	}

	links, err := netlink.LinkList()
	if err == nil {
		for _, link := range links {
			if link.Attrs().Name == interfaceName {
				fmt.Printf("Removing WireGuard interface %s\n", interfaceName)
				netlink.LinkDel(link)
				break
			}
		}
	}
}

func main() {
	interfaceName := os.Getenv("INTERFACE_NAME")
	if interfaceName == "" {
		interfaceName = "chip0"
	}
	var wireguard *netlink.Wireguard
	var ipt *iptables.IPTables

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

	dockerCIDRsString := os.Getenv("DOCKER_CIDRS")
	if dockerCIDRsString == "" {
		fmt.Printf("DOCKER_CIDRS is not set\n")
		os.Exit(ExitSetupFailed)
	}
	dockerCIDRs := strings.Split(dockerCIDRsString, ",")

	enableDockerFilterString := os.Getenv("ENABLE_DOCKER_FILTER")
	enableDockerFilter := strings.ToLower(enableDockerFilterString) == "true"

	bridgeIp := os.Getenv("BRIDGE_IP")
	bridgeInterface := os.Getenv("BRIDGE_INTERFACE")
	if bridgeInterface == "" {
		bridgeInterface = "col0"
	}

	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Panic occurred: %v\n", r)
			cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
			os.Exit(ExitSetupFailed)
		}
	}()

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

	wireguard = &netlink.Wireguard{LinkAttrs: linkAttrs}
	err = netlink.LinkAdd(wireguard)
	if err != nil {
		fmt.Printf("Could not add link %s: %v\n", linkAttrs.Name, err)
		os.Exit(ExitSetupFailed)
	}

	vmIpNet, err := netlink.ParseIPNet(vmPeerIp + "/32")
	if err != nil {
		fmt.Printf("Could not parse VM peer IPNet: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}
	hostIpNet, err := netlink.ParseIPNet(hostPeerIp + "/32")
	if err != nil {
		fmt.Printf("Could not parse host peer IPNet: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	fmt.Println("Assigning IP to WireGuard interface")

	addr := netlink.Addr{IPNet: vmIpNet, Peer: hostIpNet}
	err = netlink.AddrAdd(wireguard, &addr)
	if err != nil {
		fmt.Printf("Failed to assign IP to WireGuard interface: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	c, err := wgctrl.New()
	if err != nil {
		fmt.Printf("Failed to create wgctrl client: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	defer c.Close()

	vmPrivateKey, err := wgtypes.ParseKey(vmPrivateKeyString)
	if err != nil {
		fmt.Printf("Failed to parse VM private key: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	hostPublicKey, err := wgtypes.ParseKey(hostPublicKeyString)
	if err != nil {
		fmt.Printf("Failed to parse host public key: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	wildcardIpNet, err := netlink.ParseIPNet("0.0.0.0/0")
	if err != nil {
		fmt.Printf("Failed to parse wildcard IPNet: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	ips, err := net.LookupIP("host.docker.internal")
	if err != nil || len(ips) == 0 {
		fmt.Printf("Failed to lookup IP: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	persistentKeepaliveInterval, err := time.ParseDuration("25s")
	if err != nil {
		fmt.Printf("Failed to parse duration: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
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
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	err = netlink.LinkSetUp(wireguard)
	if err != nil {
		fmt.Printf("Failed to set wireguard link to up: %v\n", err)
		cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	ipt, err = iptables.New()
	if err != nil {
		fmt.Printf("Failed to create new iptables client: %v\n", err)
		cleanup(interfaceName, nil, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
		os.Exit(ExitSetupFailed)
	}

	fmt.Println("Adding specific iptables NAT rules for Docker networks")

	// Add specific iptables NAT rules for each Docker network CIDR
	// This restricts masquerading only to traffic destined for Docker networks
	// instead of masquerading all traffic from hostPeerIp
	for _, cidr := range dockerCIDRs {
		fmt.Printf("Adding NAT rule for Docker CIDR: %s\n", cidr)
		err = ipt.AppendUnique(
			"nat", "POSTROUTING",
			"-s", hostPeerIp,
			"-d", cidr,
			"-o", "docker+",
			"-j", "MASQUERADE",
		)
		if err != nil {
			fmt.Printf("Failed to add iptables nat rule for CIDR %s: %v\n", cidr, err)
			cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
			os.Exit(ExitSetupFailed)
		}
	}

	if enableDockerFilter {
		fmt.Println("Adding specific iptables filter rules for Docker networks")

		// Add specific iptables filter rules for each Docker network CIDR
		// This allows traffic from hostPeerIp only to specific Docker networks
		for _, cidr := range dockerCIDRs {
			fmt.Printf("Adding filter rule for Docker CIDR: %s\n", cidr)
			err = ipt.DeleteIfExists("filter", "DOCKER",
				"-s", hostPeerIp,
				"-d", cidr,
				"-o", "docker+",
				"-j", "ACCEPT")
			if err != nil {
				fmt.Printf("Failed to delete iptables filter rule for CIDR %s: %v\n", cidr, err)
				cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
				os.Exit(ExitSetupFailed)
			}
			err = ipt.Insert("filter", "DOCKER", 1,
				"-s", hostPeerIp,
				"-d", cidr,
				"-o", "docker+",
				"-j", "ACCEPT")
			if err != nil {
				fmt.Printf("Failed to insert iptables filter rule for CIDR %s: %v\n", cidr, err)
				cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
				os.Exit(ExitSetupFailed)
			}
		}
	}

	if bridgeIp != "" {
		fmt.Printf("Adding bridge traffic DOCKER-USER rules for bridge IP: %s\n", bridgeIp)

		// Add DOCKER-USER rule to accept bridge traffic from bridge IP to Docker networks
		for _, cidr := range dockerCIDRs {
			fmt.Printf("Adding DOCKER-USER rule for bridge IP %s to Docker CIDR: %s\n", bridgeIp, cidr)
			err = ipt.AppendUnique("filter", "DOCKER-USER",
				"-s", bridgeIp,
				"-d", cidr,
				"-i", bridgeInterface,
				"-o", "docker+",
				"-j", "ACCEPT")
			if err != nil {
				fmt.Printf("Failed to add DOCKER-USER rule for bridge IP %s to CIDR %s: %v\n", bridgeIp, cidr, err)
				cleanup(interfaceName, ipt, hostPeerIp, dockerCIDRs, bridgeIp, bridgeInterface)
				os.Exit(ExitSetupFailed)
			}
		}
	}

	fmt.Println("WireGuard setup completed successfully")
	os.Exit(ExitSetupSuccess)
}

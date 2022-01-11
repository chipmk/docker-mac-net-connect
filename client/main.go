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
	netlink.AddrAdd(wireguard, &addr)

	c, err := wgctrl.New()
	if err != nil {
		fmt.Printf("Failed to create wgctrl client: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	defer c.Close()

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

	ipt, err := iptables.New()
	if err != nil {
		fmt.Printf("Failed to create new iptables client: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	fmt.Println("Adding iptables NAT rule for host WireGuard IP")

	// Add iptables NAT rule to translate incoming packet's
	// source IP to the respective Docker network interface IP.
	// Required to route reply packets back through correct
	// container interface.
	err = ipt.AppendUnique(
		"nat", "POSTROUTING",
		"-s", hostPeerIp,
		"-j", "MASQUERADE",
	)
	if err != nil {
		fmt.Printf("Failed to add iptables nat rule: %v\n", err)
		os.Exit(ExitSetupFailed)
	}
}

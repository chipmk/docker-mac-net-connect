package main

import (
	"fmt"
	"net"
	"os"
	"time"

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

	fmt.Printf("Setting up interface %s\n", interfaceName)

	links, err := netlink.LinkList()
	if err != nil {
		fmt.Errorf("Could not list links: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	for _, link := range links {
		if link.Attrs().Name == interfaceName {
			fmt.Errorf("Interface %s already exists. Removing.\n", interfaceName)

			err = netlink.LinkDel(link)
			if err != nil {
				fmt.Errorf("Could not delete link %s: %v\n", interfaceName, err)
				os.Exit(ExitSetupFailed)
			}
		}
	}

	la := netlink.NewLinkAttrs()
	la.Name = interfaceName

	wireguard := &netlink.Wireguard{LinkAttrs: la}
	err = netlink.LinkAdd(wireguard)
	if err != nil {
		fmt.Printf("Could not add link %s: %v\n", la.Name, err)
	}

	ipNet, err := netlink.ParseIPNet("10.33.33.2/32")
	if err != nil {
		fmt.Printf("Could not parse IPNet: %v\n", err)
	}
	peerIpNet, err := netlink.ParseIPNet("10.33.33.1/32")
	if err != nil {
		fmt.Printf("Could not parse IPNet: %v\n", err)
	}
	addr := netlink.Addr{IPNet: ipNet, Peer: peerIpNet}
	netlink.AddrAdd(wireguard, &addr)

	c, err := wgctrl.New()
	if err != nil {
		fmt.Errorf("Failed to create wgctrl client: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	defer c.Close()

	clientPrivateKey, err := wgtypes.ParseKey("AIwSWU9veYZ2FvEG+V/sSh3DAKF3SbXCkgUHULUuNWc=")
	if err != nil {
		fmt.Errorf("Failed to parse client private key: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	serverPublicKey, err := wgtypes.ParseKey("lwiX4IRjx2GECPSzBG6efx2JEIGC7fpWDYKkj2oATWI=")
	if err != nil {
		fmt.Errorf("Failed to parse server private key: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	wildcardIpNet, err := netlink.ParseIPNet("0.0.0.0/0")
	if err != nil {
		fmt.Errorf("Failed to parse wildcard IPNet: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	ips, err := net.LookupIP("host.docker.internal")
	if err != nil || len(ips) == 0 {
		fmt.Errorf("Failed to lookup IP: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	persistentKeepaliveInterval, err := time.ParseDuration("25s")
	if err != nil {
		fmt.Errorf("Failed to parse duration: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	peer := wgtypes.PeerConfig{
		PublicKey:                   serverPublicKey,
		Endpoint:                    &net.UDPAddr{IP: ips[0], Port: 3333},
		PersistentKeepaliveInterval: &persistentKeepaliveInterval,
		AllowedIPs: []net.IPNet{
			*wildcardIpNet,
			*peerIpNet,
		},
	}

	err = c.ConfigureDevice(interfaceName, wgtypes.Config{
		PrivateKey: &clientPrivateKey,
		Peers:      []wgtypes.PeerConfig{peer},
	})
	if err != nil {
		fmt.Errorf("Failed to configure wireguard device: %v\n", err)
		os.Exit(ExitSetupFailed)
	}

	err = netlink.LinkSetUp(wireguard)
	if err != nil {
		fmt.Errorf("Failed to set wireguard link to up: %v\n", err)
		os.Exit(ExitSetupFailed)
	}
}

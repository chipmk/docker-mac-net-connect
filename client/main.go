package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func main() {
	interfaceName := "chip0"
	fmt.Printf("setting up interface %s\n", interfaceName)

	links, err := netlink.LinkList()
	if err != nil {
		fmt.Printf("could not list links: %v\n", err)
	}

	for _, link := range links {
		if link.Attrs().Name == interfaceName {
			fmt.Printf("interface %s already exists. removing\n", interfaceName)

			err = netlink.LinkDel(link)
			if err != nil {
				fmt.Printf("could not delete link %s: %v\n", interfaceName, err)
				os.Exit(1)
			}
		}
	}

	la := netlink.NewLinkAttrs()
	la.Name = interfaceName

	wireguard := &netlink.Wireguard{LinkAttrs: la}
	err = netlink.LinkAdd(wireguard)
	if err != nil {
		fmt.Printf("could not add link %s: %v\n", la.Name, err)
	}

	ipNet, err := netlink.ParseIPNet("10.33.33.2/32")
	if err != nil {
		fmt.Printf("could not parse IPNet: %v\n", err)
	}
	peerIpNet, err := netlink.ParseIPNet("10.33.33.1/32")
	if err != nil {
		fmt.Printf("could not parse IPNet: %v\n", err)
	}
	addr := netlink.Addr{IPNet: ipNet, Peer: peerIpNet}
	netlink.AddrAdd(wireguard, &addr)

	c, err := wgctrl.New()
	if err != nil {
		log.Fatalf("failed to open wgctrl: %v", err)
	}
	defer c.Close()

	clientPrivateKey, err := wgtypes.ParseKey("AIwSWU9veYZ2FvEG+V/sSh3DAKF3SbXCkgUHULUuNWc=")
	if err != nil {
		log.Fatalf("failed to generate server private key: %v", err)
	}

	serverPublicKey, err := wgtypes.ParseKey("lwiX4IRjx2GECPSzBG6efx2JEIGC7fpWDYKkj2oATWI=")
	if err != nil {
		log.Fatalf("failed to generate peer private key: %v", err)
	}

	wildcardIpNet, err := netlink.ParseIPNet("0.0.0.0/0")
	if err != nil {
		fmt.Printf("could not parse IPNet: %v\n", err)
	}

	ips, err := net.LookupIP("host.docker.internal")
	if err != nil || len(ips) == 0 {
		fmt.Fprintf(os.Stderr, "Could not get IPs: %v\n", err)
		os.Exit(1)
	}

	persistentKeepaliveInterval, err := time.ParseDuration("25s")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not parse duration: %v\n", err)
		os.Exit(1)
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

	c.ConfigureDevice(interfaceName, wgtypes.Config{
		PrivateKey: &clientPrivateKey,
		Peers:      []wgtypes.PeerConfig{peer},
	})

	netlink.LinkSetUp(wireguard)
}

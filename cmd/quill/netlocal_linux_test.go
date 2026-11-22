package main

import (
	"net/netip"
	"testing"
)

func TestLanProcParsing(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t010200C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t000200C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n" +
		"wg0\t00000000\t016433C6\t0003\t0\t0\t50\t00000000\t0\t0\t0\n" +
		"eth0\t00000000\t020200C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"
	gw := parseDefaultGateways(route)
	if gw["eth0"] != netip.MustParseAddr("192.0.2.1") || gw["wg0"] != netip.MustParseAddr("198.51.100.1") || len(gw) != 2 {
		t.Fatalf("gateways: %v", gw)
	}
	arp := "IP address       HW type     Flags       HW address            Mask     Device\n" +
		"192.0.2.1        0x1         0x2         02:00:00:00:00:01     *        eth0\n" +
		"192.0.2.7        0x1         0x0         00:00:00:00:00:00     *        eth0\n"
	m := parseARP(arp)
	if m[arpKey{netip.MustParseAddr("192.0.2.1"), "eth0"}] != testGwMAC || len(m) != 1 {
		t.Fatalf("arp: %v", m)
	}
}

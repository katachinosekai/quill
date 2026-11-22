package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// lanUnlockSupported: the home-network unlock needs the gateway MAC, which is
// read from /proc (read-only) on Linux only.
const lanUnlockSupported = true

// readNetSnap lists this PC's interfaces with their addresses and the MAC of
// each interface's IPv4 default gateway (/proc/net/route + /proc/net/arp).
func readNetSnap() netSnap {
	gw := parseDefaultGateways(readProc("/proc/net/route"))
	macs := parseARP(readProc("/proc/net/arp"))
	var out netSnap
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		n := netIface{Name: i.Name, Flags: i.Flags}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			ones, _ := ipn.Mask.Size()
			if ip.Is4() && ones > 32 {
				ones -= 96
			}
			if p := netip.PrefixFrom(ip, ones); p.IsValid() {
				n.Addrs = append(n.Addrs, p)
			}
		}
		if g, ok := gw[i.Name]; ok {
			n.GwMAC = macs[arpKey{g, i.Name}]
		}
		out.Ifaces = append(out.Ifaces, n)
	}
	return out
}

func readProc(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseDefaultGateways reads /proc/net/route: per interface, the gateway of
// its default route with the lowest metric.
func parseDefaultGateways(s string) map[string]netip.Addr {
	out := map[string]netip.Addr{}
	metric := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(s))
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x1 == 0 || flags&0x2 == 0 { // RTF_UP, RTF_GATEWAY
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		var a [4]byte
		binary.BigEndian.PutUint32(a[:], binary.LittleEndian.Uint32(b))
		m, _ := strconv.Atoi(f[6])
		if old, ok := metric[f[0]]; ok && old <= m {
			continue
		}
		metric[f[0]] = m
		out[f[0]] = netip.AddrFrom4(a)
	}
	return out
}

type arpKey struct {
	ip    netip.Addr
	iface string
}

// parseARP reads /proc/net/arp: complete entries (flag 0x2) only.
func parseARP(s string) map[arpKey]string {
	out := map[arpKey]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		ip, err := netip.ParseAddr(f[0])
		flags, err2 := strconv.ParseUint(strings.TrimPrefix(f[2], "0x"), 16, 32)
		mac, err3 := net.ParseMAC(f[3])
		if err != nil || err2 != nil || err3 != nil || flags&0x2 == 0 || len(mac) != 6 || mac.String() == "00:00:00:00:00:00" {
			continue
		}
		out[arpKey{ip, f[5]}] = mac.String()
	}
	return out
}

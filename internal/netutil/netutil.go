// Package netutil berisi utilitas jaringan bersama (enumerasi antarmuka LAN).
package netutil

import (
	"net"
	"strconv"
)

// Iface merepresentasikan satu alamat IPv4 aktif pada PC ini.
type Iface struct {
	Name      string
	IP        net.IP
	Broadcast net.IP
}

// LocalIfaces mengembalikan semua antarmuka IPv4 non-loopback yang aktif,
// beserta alamat broadcast subnet-nya. Dipakai untuk pencarian server otomatis.
func LocalIfaces() []Iface {
	var out []Iface
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			mask := ipnet.Mask
			if len(mask) != 4 {
				continue // hanya antarmuka IPv4 yang dipakai untuk siaran
			}
			bcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bcast[i] = ip4[i] | ^mask[i]
			}
			out = append(out, Iface{Name: ifc.Name, IP: ip4, Broadcast: bcast})
		}
	}
	return out
}

// LocalIPStrings mengembalikan daftar string IP lokal (untuk info status server).
func LocalIPStrings() []string {
	var out []string
	for _, f := range LocalIfaces() {
		out = append(out, f.IP.String())
	}
	return out
}

// BroadcastTargets mengembalikan semua alamat tujuan siaran UDP untuk port
// tertentu: broadcast global 255.255.255.255 + broadcast tiap subnet lokal.
// Dipakai agar tetangga di semua antarmuka (kabel/WiFi) mendengar kehadiran kita.
func BroadcastTargets(udpPort int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ip net.IP) {
		if ip == nil {
			return
		}
		s := net.JoinHostPort(ip.String(), strconv.Itoa(udpPort))
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(net.IPv4bcast) // 255.255.255.255
	for _, f := range LocalIfaces() {
		add(f.Broadcast)
	}
	return out
}

// IsLocalIP melaporkan apakah ip merupakan salah satu IP PC ini.
func IsLocalIP(ip string) bool {
	for _, f := range LocalIfaces() {
		if f.IP.String() == ip {
			return true
		}
	}
	return false
}

// IsPrivateIP melaporkan apakah ip berada dalam range privat (LAN kantor).
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	priv := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}
	for _, cidr := range priv {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

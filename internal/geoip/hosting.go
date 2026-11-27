package geoip

import "slices"

// hostingASNs are well-known cloud, hosting and VPS networks, sorted. It is a
// heuristic, not a verdict: it misses small providers, and some people sign in
// through a VPN or a company network that happens to run on one of these. It
// only makes a sign-in worth a second look. Cloudflare's own ASN (13335) is
// left out on purpose: its WARP service is a consumer VPN with many ordinary
// users, so listing it would flag normal sign-ins.
var hostingASNs = []uint32{
	8075,   // Microsoft (Azure)
	8560,   // IONOS
	9009,   // M247
	12876,  // Scaleway / Online
	14061,  // DigitalOcean
	14618,  // Amazon (AWS)
	15169,  // Google
	16276,  // OVH
	16509,  // Amazon (AWS)
	20473,  // Vultr / Choopa
	24940,  // Hetzner
	28753,  // Leaseweb
	31898,  // Oracle Cloud
	45102,  // Alibaba Cloud
	47583,  // Hostinger
	51167,  // Contabo
	60781,  // Leaseweb
	63949,  // Linode / Akamai
	132203, // Tencent Cloud
	197540, // netcup
	213230, // Hetzner
	396982, // Google Cloud
}

// IsHostingASN reports whether asn is on the built-in list of cloud and VPS
// networks.
func IsHostingASN(asn uint32) bool {
	_, ok := slices.BinarySearch(hostingASNs, asn)
	return ok
}

package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies is the set of addresses whose X-Forwarded-For may be believed.
//
// It must be an exact, fixed list — the addresses of our own proxies, written
// out in configuration. "Every private address" is not a list: an attacker who
// reaches the application from inside any private range would then be able to
// name whatever client address they like.
type TrustedProxies []netip.Prefix

// contains expects an address that has already been through parseAddr, which
// is the only way one enters this file. That is where unmapping happens, and
// it matters: an IPv4 address arriving over a dual-stack socket appears as
// ::ffff:a.b.c.d, and a 4-in-6 address is contained by no IPv4 prefix — so a
// proxy of ours would stop being recognized as one.
//
// Unmapping again here would be a second guarantee nobody can reach, which is
// worse than none: it reads as protection and is never exercised.
func (t TrustedProxies) contains(addr netip.Addr) bool {
	for _, prefix := range t {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

// ClientIP returns the address a request should be attributed to.
//
// X-Forwarded-For is sent by the client and may contain anything, so it is
// read only when the connection itself comes from a proxy we listed. The chain
// grows left to right, each hop appending the address it heard from, which
// makes the left end the least trustworthy part — and is why this walks from
// the right (ADR-0010):
//
//	XFF: 1.2.3.4, 5.6.7.8, 10.0.0.5
//	                       ^ added by our proxy, trusted
//	              ^ the actual client
//	     ^ invented by the client
//
// The second result is false when no address could be determined at all,
// which is a caller the limiter must refuse rather than let through unkeyed.
func ClientIP(r *http.Request, trusted TrustedProxies) (netip.Addr, bool) {
	socket, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}

	if !trusted.contains(socket) {
		// The connection is not from one of our proxies, so whatever the
		// header says is the client talking about itself.
		return socket, true
	}

	hops := forwardedFor(r)

	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseAddr(hops[i])
		if !ok {
			// Something unparseable sits in the chain, so nothing further left
			// can be attributed to anyone. Falling back to the socket address
			// buckets everyone behind that proxy together, which throttles
			// innocent traffic — the wrong direction, but the safe one. The
			// alternative is keying on a value the caller chose.
			return socket, true
		}

		if !trusted.contains(hop) {
			return hop, true
		}
	}

	// Every hop was one of ours, so the client's own address was never
	// recorded. There is nothing better than the socket to key on.
	return socket, true
}

// forwardedFor flattens every X-Forwarded-For header into one ordered chain.
//
// A proxy may append its own header rather than extending the existing one, so
// reading only the first would silently drop hops — and dropping hops from the
// right is exactly what shifts which entry gets read as the client.
func forwardedFor(r *http.Request) []string {
	var hops []string

	for _, header := range r.Header.Values("X-Forwarded-For") {
		for hop := range strings.SplitSeq(header, ",") {
			if trimmed := strings.TrimSpace(hop); trimmed != "" {
				hops = append(hops, trimmed)
			}
		}
	}

	return hops
}

// parseAddr accepts both "host:port" and a bare address, because RemoteAddr
// carries a port and a header entry does not.
//
// The zone on a link-local address is dropped: it is meaningful only on the
// machine that wrote it, and keeping it would split one client across buckets
// depending on which interface a packet arrived through.
func parseAddr(raw string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}

	// A bracketed IPv6 literal without a port, which some proxies write.
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")

	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, false
	}

	return addr.Unmap().WithZone(""), true
}

// RateLimitKey turns an address into the key one bucket counts against.
//
// IPv6 is aggregated to /64 (ADR-0010). A single subscriber is normally handed
// a whole /64, so counting per address would hand them billions of free
// attempts — the limit would be decorative. IPv4 is counted per address: it is
// scarce enough that a single one rarely belongs to one person, and
// aggregating further would sweep in whole CGNAT populations.
//
// This is why an address is never the only bucket. docs/adr/0010 keeps account
// and address as separate counters for exactly that reason.
func RateLimitKey(addr netip.Addr) string {
	if addr.Is6() && !addr.Is4In6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}

	return addr.Unmap().String()
}

// The network an address belongs to, for the bucket that counts a whole
// network rather than one caller.
//
// /24 and /48 are the same two boundaries ADR-0010 already names for truncating
// addresses in long-term logs, and reusing them means this application has one
// notion of "the network this address is part of" rather than two that drift.
//
// /24 is the smallest IPv4 block that is still routed as a unit, so it is the
// smallest thing an attacker cannot assemble out of unrelated addresses. /48 is
// a whole site allocation in IPv6 — one organization — which sits exactly one
// level above the /64 RateLimitKey already treats as one caller.
const (
	ipv4NetworkBits = 24
	ipv6NetworkBits = 48
)

// RateLimitPrefixKey turns an address into the key its network's bucket counts
// against.
//
// This exists because the per-address bucket is defeated by spreading the work
// out. Thirty attempts from each of two hundred addresses stays under every
// per-address limit and is still six thousand guesses, and a rented /24 or a
// single IPv6 site costs an attacker almost nothing. Counting the network
// catches the spread; counting only the address never can.
//
// It is deliberately not the only bucket either. A whole university or ISP
// block can sit inside one /24, so this number has to stay loose enough that
// honest traffic never reaches it — which is why the account bucket, the one
// that can actually be tight, exists alongside it.
func RateLimitPrefixKey(addr netip.Addr) string {
	// Unmapped first: a 4-in-6 address is IPv4 wearing an IPv6 shape, and
	// taking 48 bits of it would key on part of the ::ffff: prefix that every
	// mapped address shares — one bucket for the entire IPv4 internet.
	addr = addr.Unmap()

	bits := ipv4NetworkBits
	if addr.Is6() {
		bits = ipv6NetworkBits
	}

	prefix, err := addr.Prefix(bits)
	if err != nil {
		// Only reachable for an address whose bit length is smaller than the
		// mask, which netip does not produce from either parse path. Keying on
		// the address itself is narrower than intended rather than wider, so it
		// throttles less than it should instead of throttling strangers.
		return addr.String()
	}

	return prefix.String()
}

package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

func prefixes(t *testing.T, raw ...string) httpx.TrustedProxies {
	t.Helper()

	out := make(httpx.TrustedProxies, 0, len(raw))

	for _, r := range raw {
		p, err := netip.ParsePrefix(r)
		if err != nil {
			t.Fatalf("ParsePrefix(%q): %v", r, err)
		}

		out = append(out, p)
	}

	return out
}

func request(t *testing.T, remote string, forwarded ...string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = remote

	for _, header := range forwarded {
		req.Header.Add("X-Forwarded-For", header)
	}

	return req
}

func clientIP(t *testing.T, r *http.Request, trusted httpx.TrustedProxies) string {
	t.Helper()

	addr, ok := httpx.ClientIP(r, trusted)
	if !ok {
		return ""
	}

	return addr.String()
}

// The header is sent by the client. A connection that did not come from one of
// our proxies gets no say in what address it is counted against — otherwise
// every rate limit in the application is one header away from being useless.
func TestTheHeaderIsIgnoredWhenTheConnectionIsNotFromAProxy(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	r := request(t, "203.0.113.9:41234", "1.2.3.4")

	if got := clientIP(t, r, trusted); got != "203.0.113.9" {
		t.Errorf("client = %q, want the socket address", got)
	}
}

// With no trusted proxies configured at all — which is the local and
// pre-deploy state — nothing may ever be read from the header.
func TestWithNoTrustedProxiesTheHeaderIsNeverRead(t *testing.T) {
	t.Parallel()

	r := request(t, "198.51.100.7:9000", "1.2.3.4, 5.6.7.8")

	if got := clientIP(t, r, nil); got != "198.51.100.7" {
		t.Errorf("client = %q, want the socket address", got)
	}
}

// The chain grows left to right, so the rightmost untrusted hop is the client.
// Reading from the left instead would take whatever the caller invented.
func TestTheChainIsWalkedFromTheRight(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	r := request(t, "10.0.0.5:41234", "1.2.3.4, 5.6.7.8, 10.0.0.5")

	if got := clientIP(t, r, trusted); got != "5.6.7.8" {
		t.Errorf("client = %q, want 5.6.7.8 — the first untrusted hop from the right", got)
	}
}

// A caller who prepends addresses cannot shift which entry gets read, because
// the walk starts at the end and stops at the first hop that is not ours.
func TestAForgedPrefixCannotMoveTheReadPosition(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	forged := request(t, "10.0.0.5:1", "10.0.0.5, 10.0.0.5, 10.0.0.5, 203.0.113.1, 10.0.0.5")
	honest := request(t, "10.0.0.5:1", "203.0.113.1, 10.0.0.5")

	if got, want := clientIP(t, forged, trusted), clientIP(t, honest, trusted); got != want {
		t.Errorf("forged chain resolved to %q, honest chain to %q", got, want)
	}
}

// Some proxies append a second header instead of extending the first. Reading
// only one would drop hops from the right, which is exactly what changes the
// answer.
func TestHopsSpreadAcrossSeveralHeadersAreOneChain(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	r := request(t, "10.0.0.5:1", "1.2.3.4", "203.0.113.1, 10.0.0.5")

	if got := clientIP(t, r, trusted); got != "203.0.113.1" {
		t.Errorf("client = %q, want 203.0.113.1", got)
	}
}

// Unparseable input in the chain means nothing further left can be attributed
// to anybody. Falling back to the socket throttles innocent traffic together —
// the wrong direction, but the safe one; the alternative keys on a value the
// caller chose.
func TestGarbageInTheChainFallsBackToTheSocket(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	r := request(t, "10.0.0.5:1", "1.2.3.4, not-an-ip, 10.0.0.5")

	if got := clientIP(t, r, trusted); got != "10.0.0.5" {
		t.Errorf("client = %q, want the socket address", got)
	}
}

// Every hop ours means the client's own address was never recorded, so there
// is nothing better than the socket to key on.
func TestAChainOfOnlyOurOwnProxiesFallsBackToTheSocket(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	r := request(t, "10.0.0.5:1", "10.0.0.4, 10.0.0.5")

	if got := clientIP(t, r, trusted); got != "10.0.0.5" {
		t.Errorf("client = %q, want the socket address", got)
	}
}

// An IPv4 address arriving over a dual-stack socket appears as ::ffff:a.b.c.d.
// Left mapped, it is contained by no IPv4 prefix — so a proxy of ours would
// stop being recognized as one, and the header would go unread.
func TestAnIPv4AddressMappedIntoIPv6IsStillThatAddress(t *testing.T) {
	t.Parallel()

	trusted := prefixes(t, "10.0.0.0/8")

	r := request(t, "[::ffff:10.0.0.5]:41234", "203.0.113.1, 10.0.0.5")

	if got := clientIP(t, r, trusted); got != "203.0.113.1" {
		t.Errorf("client = %q, want the header to have been read", got)
	}
}

// A zone is meaningful only on the machine that wrote it. Keeping it would
// split one client across buckets depending on the interface a packet arrived
// through.
func TestTheInterfaceZoneIsDropped(t *testing.T) {
	t.Parallel()

	r := request(t, "[fe80::1%eth0]:41234")

	if got := clientIP(t, r, nil); got != "fe80::1" {
		t.Errorf("client = %q, want the zone dropped", got)
	}
}

// A request whose address cannot be read at all must be refused rather than
// counted against an empty key, which would put every such request in one
// bucket with every other.
func TestAnUnreadableAddressIsReportedRatherThanGuessed(t *testing.T) {
	t.Parallel()

	if _, ok := httpx.ClientIP(request(t, "not-an-address"), nil); ok {
		t.Error("an unreadable RemoteAddr was accepted")
	}
}

// ADR-0010: a subscriber is normally handed a whole IPv6 /64, so counting per
// address would hand them billions of free attempts.
func TestIPv6IsCountedPerSixtyFour(t *testing.T) {
	t.Parallel()

	first := netip.MustParseAddr("2001:db8:1:2::1")
	second := netip.MustParseAddr("2001:db8:1:2:ffff:ffff:ffff:ffff")
	other := netip.MustParseAddr("2001:db8:1:3::1")

	if httpx.RateLimitKey(first) != httpx.RateLimitKey(second) {
		t.Error("two addresses in one /64 were counted separately")
	}

	if httpx.RateLimitKey(first) == httpx.RateLimitKey(other) {
		t.Error("two different /64s were counted together")
	}
}

// IPv4 is counted per address. Aggregating further would sweep whole CGNAT
// populations into one bucket, and in Indonesia that is thousands of people
// behind one address.
func TestIPv4IsCountedPerAddress(t *testing.T) {
	t.Parallel()

	neighbor := netip.MustParseAddr("203.0.113.2")

	if key := httpx.RateLimitKey(netip.MustParseAddr("203.0.113.1")); key != "203.0.113.1" {
		t.Errorf("key = %q, want the address itself", key)
	}

	if httpx.RateLimitKey(netip.MustParseAddr("203.0.113.1")) == httpx.RateLimitKey(neighbor) {
		t.Error("two IPv4 neighbors share a bucket")
	}
}

// The mapped form must key the same as the plain one, or the same client
// counted through two sockets would get two budgets.
func TestAMappedIPv4KeysTheSameAsThePlainOne(t *testing.T) {
	t.Parallel()

	plain := netip.MustParseAddr("203.0.113.1")
	mapped := netip.MustParseAddr("::ffff:203.0.113.1")

	if httpx.RateLimitKey(plain) != httpx.RateLimitKey(mapped) {
		t.Errorf("plain %q and mapped %q key differently",
			httpx.RateLimitKey(plain), httpx.RateLimitKey(mapped))
	}
}

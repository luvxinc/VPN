package geoip

import (
	"net"
	"strings"
	"sync"

	"github.com/oschwald/geoip2-golang"
)

var (
	once   sync.Once
	reader *geoip2.Reader
)

var privateIPPrefixes = []string{
	"127.", "192.168.", "10.", "172.",
	"::1", "localhost", "0:0:0:0:0:0:0:1",
}

// Init opens the GeoLite2 database. Safe to call multiple times (sync.Once).
func Init(dbPath string) {
	once.Do(func() {
		r, err := geoip2.Open(dbPath)
		if err == nil {
			reader = r
		}
		// Missing DB → reader stays nil → LookupIP returns ("Unknown","Unknown")
	})
}

// LookupIP returns (country, city) for an IP address.
// Private/loopback IPs return ("Local","Local").
// Missing DB or lookup errors return ("Unknown","Unknown").
func LookupIP(ip string) (country, city string) {
	for _, prefix := range privateIPPrefixes {
		if strings.HasPrefix(ip, prefix) {
			return "Local", "Local"
		}
	}
	if reader == nil {
		return "Unknown", "Unknown"
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "Unknown", "Unknown"
	}
	record, err := reader.City(parsed)
	if err != nil {
		return "Unknown", "Unknown"
	}
	c := record.Country.Names["en"]
	if c == "" {
		c = "Unknown"
	}
	ci := record.City.Names["en"]
	if ci == "" {
		ci = "Unknown"
	}
	return c, ci
}

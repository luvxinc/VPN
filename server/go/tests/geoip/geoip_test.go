package geoip_test

import (
	"testing"

	"github.com/luvxinc/vpn/server/geoip"
	"github.com/stretchr/testify/assert"
)

func TestLookupIP_PrivateAddresses(t *testing.T) {
	privateIPs := []string{
		"127.0.0.1",
		"192.168.1.1",
		"10.0.0.1",
		"172.16.0.1",
		"172.20.0.1",
		"::1",
		"localhost",
	}
	for _, ip := range privateIPs {
		country, city := geoip.LookupIP(ip)
		assert.Equal(t, "Local", country, "IP %s should be Local", ip)
		assert.Equal(t, "Local", city, "IP %s should be Local", ip)
	}
}

func TestLookupIP_NoDB(t *testing.T) {
	// geoip.Init not called with a real DB path → reader is nil
	// Any public IP should return Unknown
	country, city := geoip.LookupIP("8.8.8.8")
	assert.Equal(t, "Unknown", country)
	assert.Equal(t, "Unknown", city)
}

func TestLookupIP_InvalidIP(t *testing.T) {
	country, city := geoip.LookupIP("not-an-ip")
	assert.Equal(t, "Unknown", country)
	assert.Equal(t, "Unknown", city)
}

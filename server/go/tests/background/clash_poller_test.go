package background_test

import (
	"encoding/json"
	"testing"

	"github.com/luvxinc/vpn/server/background"
	"github.com/stretchr/testify/assert"
)

// TestExtractVlessUUID_InboundUser tests the primary UUID extraction path
// (sing-box 1.13+ provides metadata.inboundUser.uuid).
func TestExtractVlessUUID_InboundUser(t *testing.T) {
	conn := map[string]json.RawMessage{}
	meta := map[string]interface{}{
		"inboundUser": map[string]string{
			"uuid": "aabbccdd-1234-5678-abcd-ef0123456789",
		},
	}
	metaJSON, _ := json.Marshal(meta)
	conn["metadata"] = json.RawMessage(metaJSON)

	uuid := background.ExtractVlessUUID(conn)
	assert.Equal(t, "aabbccdd-1234-5678-abcd-ef0123456789", uuid)
}

// TestExtractVlessUUID_Chains tests fallback UUID extraction via chains array.
func TestExtractVlessUUID_Chains(t *testing.T) {
	conn := map[string]json.RawMessage{}
	// No inboundUser in metadata
	conn["metadata"] = json.RawMessage(`{}`)
	// chains contains a UUID-shaped string
	chains := []string{"direct", "11223344-aaaa-bbbb-cccc-ddeeff001122", "proxy"}
	chainsJSON, _ := json.Marshal(chains)
	conn["chains"] = json.RawMessage(chainsJSON)

	uuid := background.ExtractVlessUUID(conn)
	assert.Equal(t, "11223344-aaaa-bbbb-cccc-ddeeff001122", uuid)
}

// TestExtractVlessUUID_NoUUID tests that empty string is returned when no UUID found.
func TestExtractVlessUUID_NoUUID(t *testing.T) {
	conn := map[string]json.RawMessage{
		"metadata": json.RawMessage(`{"host":"example.com"}`),
		"chains":   json.RawMessage(`["direct","proxy"]`),
	}
	uuid := background.ExtractVlessUUID(conn)
	assert.Equal(t, "", uuid)
}

func TestNormalizeHost_StripPort(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com:443", "example.com"},
		{"example.com:8080", "example.com"},
		{"example.com", "example.com"},
		{"192.168.1.1:80", "192.168.1.1"},
		{"[::1]:443", "[::1]:443"}, // IPv6 literal, not stripped
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, background.NormalizeHost(tt.input), "input: %s", tt.input)
	}
}

func TestNormalizeHost_Truncate(t *testing.T) {
	long := string(make([]byte, 300))
	for i := range long {
		long = long[:i] + "a" + long[i+1:]
	}
	result := background.NormalizeHost(long)
	assert.LessOrEqual(t, len(result), 253)
}

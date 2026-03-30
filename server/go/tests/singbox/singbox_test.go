package singbox_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/luvxinc/vpn/server/singbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Replace pkill with a no-op in tests
	singbox.ExecFunc = func(name string, args ...string) error { return nil }
}

func writeTempConfig(t *testing.T, content interface{}) string {
	t.Helper()
	data, err := json.Marshal(content)
	require.NoError(t, err)
	f, err := os.CreateTemp("", "singbox-test-*.json")
	require.NoError(t, err)
	f.Write(data)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func readConfig(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestUpdateUUID_ReplacesUUID(t *testing.T) {
	original := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type": "vless",
				"users": []interface{}{
					map[string]string{"uuid": "old-uuid-1234-5678-abcd-ef0123456789", "flow": "xtls-rprx-vision"},
				},
			},
		},
	}
	path := writeTempConfig(t, original)

	newUUID := "new-uuid-aaaa-bbbb-cccc-dddd11223344"
	err := singbox.UpdateUUID(path, newUUID)
	require.NoError(t, err)

	result := readConfig(t, path)
	inbounds := result["inbounds"].([]interface{})
	users := inbounds[0].(map[string]interface{})["users"].([]interface{})
	assert.Equal(t, newUUID, users[0].(map[string]interface{})["uuid"])
	assert.Equal(t, "xtls-rprx-vision", users[0].(map[string]interface{})["flow"])
}

func TestUpdateUUID_PreservesOtherFields(t *testing.T) {
	original := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type":        "vless",
				"tag":         "vless-in",
				"listen":      "::",
				"listen_port": 443,
				"users":       []interface{}{},
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{"type": "direct", "tag": "direct"},
		},
		"experimental": map[string]interface{}{
			"clash_api": map[string]interface{}{"external_controller": "127.0.0.1:9090"},
		},
	}
	path := writeTempConfig(t, original)

	err := singbox.UpdateUUID(path, "new-uuid-test-1234-5678-abcdef012345")
	require.NoError(t, err)

	result := readConfig(t, path)
	// Other fields should be preserved
	assert.NotNil(t, result["outbounds"])
	assert.NotNil(t, result["experimental"])
	inbounds := result["inbounds"].([]interface{})
	inbound0 := inbounds[0].(map[string]interface{})
	assert.Equal(t, "vless", inbound0["type"])
	assert.Equal(t, "vless-in", inbound0["tag"])
}

func TestUpdateUUID_AtomicRename(t *testing.T) {
	original := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type":  "vless",
				"users": []interface{}{},
			},
		},
	}
	path := writeTempConfig(t, original)

	err := singbox.UpdateUUID(path, "test-uuid-0000-1111-2222-333344445555")
	require.NoError(t, err)

	// Temp file should be gone (renamed away)
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp file should not exist after rename")

	// Original path should exist
	_, err = os.Stat(path)
	assert.NoError(t, err)
}

func TestUpdateUUID_MissingFile(t *testing.T) {
	err := singbox.UpdateUUID("/nonexistent/path/config.json", "some-uuid")
	assert.Error(t, err)
}

func TestUpdateUUID_NoInbounds(t *testing.T) {
	path := writeTempConfig(t, map[string]interface{}{"outbounds": []interface{}{}})
	err := singbox.UpdateUUID(path, "some-uuid")
	assert.Error(t, err)
}

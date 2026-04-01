package handlers

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// InstallScript returns a bash script that downloads and installs the macOS client.
// Accessible via: curl -fsSL https://<domain>/install | bash
func (h *APIHandler) InstallScript(c *fiber.Ctx) error {
	// Dynamically resolve the download URL based on the request host
	protocol := "https"
	if strings.Contains(c.Hostname(), "localhost") || strings.Contains(c.Hostname(), "127.0.0.1") {
		protocol = "http"
	}
	downloadURL := fmt.Sprintf("%s://%s/download/client", protocol, c.Hostname())

	script := `#!/usr/bin/env bash
set -e

echo "================================================="
echo "   🚀 开始安装 为爱鼓掌 (WeiAi) VPN 客户端..."
echo "================================================="

# 1. 检查操作系统
if [ "$(uname -s)" != "Darwin" ]; then
    echo "❌ 错误: 此客户端仅支持 macOS (Darwin) 系统。"
    exit 1
fi

# 2. 定义临时挂载点
TMP_DIR=$(mktemp -d)
ZIP_PATH="$TMP_DIR/weiai-client.zip"
APP_DIR="/Applications/为爱鼓掌.app"

# 3. 下载最新客户端包
echo "⏳ 正在从云端拉取客户端核心..."
curl -# -fL -o "$ZIP_PATH" "` + downloadURL + `"

# 4. 解压并安置
echo "📦 正在进行强制部署 (首次可能需输入 Mac 开机密码)..."
sudo rm -rf "$APP_DIR"
sudo unzip -q -o "$ZIP_PATH" -d "/Applications"

# 5. 解除苹果的安全隔离 (去除烂苹果标志)
echo "🔓 正在扫除系统网闸限制..."
sudo xattr -cr "$APP_DIR"

# 6. 清理兵营
rm -rf "$TMP_DIR"

echo "================================================="
echo "✅ 安装圆满成功！"
echo "✅ 应用已驻扎: $APP_DIR"
echo "🚀 正在自动打开应用，祝您冲浪愉快！"
echo "================================================="

# 7. 凯旋点火 (直接拉起界面)
open -a "$APP_DIR"
`
	c.Type("text/plain", "utf-8")
	return c.SendString(script)
}

#!/bin/bash
set -e

DISPLAY_NAME="为爱鼓掌"   # 显示名称（中文）
EXEC_NAME="WeiAiVPN"       # 可执行文件名（必须 ASCII，否则 macOS 无法启动）
BUNDLE_ID="com.weiai.vpn"
VERSION="1.0.0"

echo "=== 构建 ${DISPLAY_NAME}.app ==="

# 1. 编译 Swift
swift build -c release 2>&1
BINARY=".build/release/WeiAiApp"

# 2. 下载 sing-box（如果还没有）
SINGBOX_URL="https://github.com/SagerNet/sing-box/releases/download/v1.13.4/sing-box-1.13.4-darwin-arm64.tar.gz"
if [ ! -f "Resources/sing-box" ]; then
    echo "下载 sing-box..."
    mkdir -p Resources
    curl -Lo /tmp/sb.tar.gz "$SINGBOX_URL"
    tar -xzf /tmp/sb.tar.gz -C /tmp
    cp /tmp/sing-box-1.13.4-darwin-arm64/sing-box Resources/sing-box
    chmod +x Resources/sing-box
fi

# 3. 创建 .app bundle 结构
APP_DIR="dist/${DISPLAY_NAME}.app"
rm -rf "$APP_DIR"
mkdir -p "${APP_DIR}/Contents/MacOS"
mkdir -p "${APP_DIR}/Contents/Resources"

# 4. 复制文件
cp "$BINARY"              "${APP_DIR}/Contents/MacOS/${EXEC_NAME}"
cp "Resources/sing-box"   "${APP_DIR}/Contents/Resources/sing-box"
if [ -f "Resources/AppIcon.icns" ]; then
    cp "Resources/AppIcon.icns" "${APP_DIR}/Contents/Resources/AppIcon.icns"
fi
# Bundle config.json (gitignored — copy from config.example.json if missing)
if [ -f "Resources/config.json" ]; then
    cp "Resources/config.json" "${APP_DIR}/Contents/Resources/config.json"
elif [ -f "Resources/config.example.json" ]; then
    echo "警告：未找到 Resources/config.json，使用示例配置（请在 Resources/ 创建真实的 config.json）"
    cp "Resources/config.example.json" "${APP_DIR}/Contents/Resources/config.json"
fi
cp "Resources/weiai-helper.sh" "${APP_DIR}/Contents/Resources/weiai-helper.sh"
chmod +x "${APP_DIR}/Contents/MacOS/${EXEC_NAME}"
chmod +x "${APP_DIR}/Contents/Resources/sing-box"
chmod +x "${APP_DIR}/Contents/Resources/weiai-helper.sh"

# 4b. 复制本地化文件
for lproj in en.lproj zh-Hans.lproj; do
    if [ -d "Resources/$lproj" ]; then
        cp -R "Resources/$lproj" "${APP_DIR}/Contents/Resources/$lproj"
    fi
done

# 5. 写 Info.plist（EXEC_NAME 必须与 Contents/MacOS/ 下文件名一致）
cat > "${APP_DIR}/Contents/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleIdentifier</key>        <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>              <string>${DISPLAY_NAME}</string>
    <key>CFBundleDisplayName</key>       <string>${DISPLAY_NAME}</string>
    <key>CFBundleVersion</key>           <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key><string>${VERSION}</string>
    <key>CFBundleExecutable</key>        <string>${EXEC_NAME}</string>
    <key>CFBundlePackageType</key>       <string>APPL</string>
    <key>LSUIElement</key>               <true/>
    <key>NSHighResolutionCapable</key>   <true/>
    <key>LSMinimumSystemVersion</key>    <string>13.0</string>
    <key>CFBundleIconFile</key>          <string>AppIcon</string>
    <key>NSPrincipalClass</key>          <string>NSApplication</string>
    <key>CFBundleDevelopmentRegion</key> <string>en</string>
    <!-- 允许连接自签名 HTTPS（真正的安全由 certificate pinning 保证） -->
    <key>NSAppTransportSecurity</key>
    <dict>
        <key>NSAllowsArbitraryLoads</key>
        <true/>
    </dict>
</dict>
</plist>
EOF

# 6. Ad-hoc 代码签名
echo "代码签名..."
codesign --force --deep --sign - "${APP_DIR}" 2>&1

# 7. 移除隔离标记（必须在打 zip 之前做，否则用户解压后仍会触发 Gatekeeper）
echo "移除隔离标记..."
xattr -cr "${APP_DIR}"

# 8. 打包 zip
echo "打包 zip..."
cd dist && zip -r "${DISPLAY_NAME}.zip" "${DISPLAY_NAME}.app" -q && cd ..

echo ""
echo "✓ 完成: dist/${DISPLAY_NAME}.zip  （可直接发给用户）"

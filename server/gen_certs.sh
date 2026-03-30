#!/bin/bash
# 生成自签名证书（有效期10年）
# 用法: bash gen_certs.sh YOUR_SERVER_IP
set -e
SERVER_IP="${1:?Usage: bash gen_certs.sh YOUR_SERVER_IP}"

mkdir -p certs
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key \
  -out certs/server.crt -days 3650 -nodes \
  -subj "/CN=${SERVER_IP}" \
  -addext "subjectAltName=IP:${SERVER_IP}"

# 输出证书指纹（客户端 pinning 用）
echo ""
echo "=== 证书 SHA256 指纹（复制到客户端 config.json 的 cert_fingerprint）==="
openssl x509 -in certs/server.crt -fingerprint -sha256 -noout \
  | sed 's/SHA256 Fingerprint=//' | tr -d ':'

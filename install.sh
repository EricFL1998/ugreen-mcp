#!/usr/bin/env bash
# ==============================================================================
#  绿联 NAS (UGOS Pro) 专属 MCP 服务端一键安装脚本
#  GitHub: https://github.com/EricFL1998/ugreen-mcp
# ==============================================================================
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}================================================================================${NC}"
echo -e "${BLUE}       正在安装 UGREEN NAS (UGOS Pro) MCP 服务端 (常驻开机自启)...       ${NC}"
echo -e "${BLUE}================================================================================${NC}"

# 1. 检查 root 权限
if [ "$(id -u)" != "0" ]; then
    echo -e "${RED}[ERROR] 请使用 sudo 执行此脚本: sudo bash install.sh${NC}"
    exit 1
fi

# 2. 探测 NAS 本地局域网 IP
NAS_IP=$(ip -4 route get 8.8.8.8 2>/dev/null | awk '{print $7}' | tr -d ' \n')
if [ -z "$NAS_IP" ]; then
    NAS_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
fi
if [ -z "$NAS_IP" ]; then
    NAS_IP="<你的NAS_IP>"
fi

TARGET_BIN="/usr/local/bin/ugreen-nas-mcp"
SERVICE_FILE="/etc/systemd/system/ugreen-nas-mcp.service"
CONFIG_DIR="/etc/ugreen-nas-mcp"
LOG_DIR="/var/log/ugreen-nas-mcp"
RELEASE_URL="https://github.com/EricFL1998/ugreen-mcp/releases/download/v1.0.0/ugreen-nas-mcp-linux-amd64"

# 3. 创建目录
mkdir -p "$CONFIG_DIR" "$LOG_DIR"

# 4. 下载或安装二进制
echo -e "${YELLOW}[1/4] 正在下载适配 UGOS Pro (x86_64) 的静态二进制文件...${NC}"
TMP_BIN="/tmp/ugreen-nas-mcp-download.$$"
if command -v curl >/dev/null 2>&1; then
    curl -fSL --progress-bar "$RELEASE_URL" -o "$TMP_BIN"
elif command -v wget >/dev/null 2>&1; then
    wget -q --show-progress "$RELEASE_URL" -O "$TMP_BIN"
else
    echo -e "${RED}[ERROR] 系统未安装 curl 或 wget，请先安装后再试${NC}"
    exit 1
fi

chmod +x "$TMP_BIN"
mv "$TMP_BIN" "$TARGET_BIN"
echo -e "${GREEN}[✓] 二进制文件已就绪: $TARGET_BIN${NC}"

# 5. 注册 systemd 单元
echo -e "${YELLOW}[2/4] 正在注册 systemd 开机自启守护进程...${NC}"
cat > "$SERVICE_FILE" << 'EOF'
[Unit]
Description=UGREEN NAS MCP Server (UGOS Pro)
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
User=root
UnsetEnvironment=GODEBUG
Environment="GODEBUG="
ExecStart=/usr/local/bin/ugreen-nas-mcp --port 8095
Restart=always
RestartSec=5s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now ugreen-nas-mcp.service

echo -e "${YELLOW}[3/4] 正在启动服务并校验健康状态...${NC}"
sleep 2

# 6. 获取现场动态生成的密钥并展示配置
AUTH_KEY=$("$TARGET_BIN" --show-key 2>/dev/null || echo "")
if [ -z "$AUTH_KEY" ]; then
    AUTH_KEY=$(grep -o '"auth_token": *"[^"]*"' /etc/ugreen-nas-mcp/config.json 2>/dev/null | cut -d'"' -f4 || echo "")
fi

echo -e "${GREEN}[4/4] [✓] 安装完成！服务已常驻运行在后台并设置为开机自启！${NC}"
echo ""
echo -e "${BLUE}================================================================================${NC}"
echo -e "${GREEN}       ������ 恭喜！绿联 NAS (UGOS Pro) MCP 服务已成功部署！${NC}"
echo -e "${BLUE}================================================================================${NC}"
echo -e " ������ [MCP 服务地址] : ${YELLOW}http://$NAS_IP:8095/sse${NC}"
echo -e " --------------------------------------------------------------------------------"
echo -e " ������ [现场动态生成的 Authorization Key (注册凭证)]:"
echo -e "    ${GREEN}Bearer $AUTH_KEY${NC}"
echo -e " --------------------------------------------------------------------------------"
echo -e " ������ [直接复制此 JSON 到 Cursor / Claude Desktop / VS Code 即可完成注册]:"
cat << EOF

{
  "mcpServers": {
    "ugreen-nas": {
      "url": "http://$NAS_IP:8095/sse",
      "headers": {
        "Authorization": "Bearer $AUTH_KEY"
      }
    }
  }
}

EOF
echo -e "${BLUE}================================================================================${NC}"
echo -e "${GREEN}✨ 以后日常管理 NAS 完全不需要再打开 SSH 终端，直接在 AI 对话框中自然语言操控即可！${NC}"

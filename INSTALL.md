# 绿联 NAS (UGOS Pro) 专属 MCP 服务安装与注册使用指南

本文档介绍如何为你的绿联 NAS (UGOS Pro) 安装配置专属 MCP 服务，以及如何在各类 AI 客户端（Cursor、Claude Desktop、VS Code / Cline、Codex 等）中完成注册接入，彻底告别 SSH 终端黑窗口。

---

## 1. 架构与接入总览

- **NAS 服务端信息**：
  - **局域网访问地址**：`http://<nas_ip>:8095/sse`
  - **默认鉴权 Token**：`ugreen_mcp_<your_generated_key>`
  - **通信协议**：MCP (Model Context Protocol) 2024-11-05 标准 Server-Sent Events (SSE)
  - **宿主环境**：systemd 守护进程常驻（开机自启，内存占用约 2.3MB，崩溃秒级拉起）
- **核心防护体系**：
  - **Bearer Token 访问鉴权**：杜绝局域网内未授权访问。
  - **二阶段高危确认机制**：删除文件、重启生产容器（Emby/MoviePilot 等）、重启系统应用时强制生成短效 60 秒票据拦截，必须由用户在聊天窗口中明确“确认”后才执行。
  - **数据卷白名单沙箱**：严格锁定在 `/volume1/` 合法目录，严禁遍历或篡改系统根目录与底座私有分区。

---

## 2. 各主流 AI 客户端注册指南

将以下配置复制粘贴到对应客户端的配置文件中即可完成接入。

### 2.1 Cursor
1. 打开 Cursor，点击右上角齿轮图标进入 **Settings**（设置）。
2. 在左侧菜单点击 **Features** -> 找到 **MCP Servers**。
3. 点击 **+ Add New MCP Server**：
   - **Name**：`ugreen-nas`
   - **Type**：`sse`
   - **Server URL**：`http://<nas_ip>:8095/sse`
   - **Headers**：
     - Key: `Authorization`
     - Value: `Bearer ugreen_mcp_<your_generated_key>`
4. 或者直接在 `~/.cursor/mcp.json` 中粘贴配置：
```json
{
  "mcpServers": {
    "ugreen-nas": {
      "url": "http://<nas_ip>:8095/sse",
      "headers": {
        "Authorization": "Bearer ugreen_mcp_<your_generated_key>"
      }
    }
  }
}
```
5. 保存后，Cursor 会显示绿色指示灯，表示 22+ 个 NAS 管理工具已就绪。

---

### 2.2 Claude Desktop (官方客户端)
1. 打开 Claude Desktop。
2. 点击菜单 **Claude** -> **Settings** -> **Developer** -> 点击 **Edit Config**（编辑 `claude_desktop_config.json`）。
3. 填入如下配置：
```json
{
  "mcpServers": {
    "ugreen-nas": {
      "url": "http://<nas_ip>:8095/sse",
      "headers": {
        "Authorization": "Bearer ugreen_mcp_<your_generated_key>"
      }
    }
  }
}
```
4. 重启 Claude Desktop，在输入框右下方将看到螺丝刀工具图标，显示 `ugreen-nas` 已连接。

---

### 2.3 VS Code (Cline / Roo Code / Continue 插件)
以 Cline 插件为例：
1. 点击侧边栏 Cline 插件图标，进入设置（Settings）。
2. 找到 **MCP Servers** 选项卡，点击右侧的 **Configure MCP Servers** 按钮打开配置 JSON。
3. 填入如下内容：
```json
{
  "mcpServers": {
    "ugreen-nas": {
      "url": "http://<nas_ip>:8095/sse",
      "headers": {
        "Authorization": "Bearer ugreen_mcp_<your_generated_key>"
      }
    }
  }
}
```
4. 保存后，插件即可调用绿联 NAS 的全部工具集。

---

### 2.4 通用 Agent / 自建大模型工具 (Python / LangChain)
```python
import requests

url = "http://<nas_ip>:8095/message"
headers = {
    "Authorization": "Bearer ugreen_mcp_<your_generated_key>",
    "Content-Type": "application/json"
}

# 调用系统状态示例
payload = {
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
        "name": "nas_system_get_overview",
        "arguments": {}
    }
}
response = requests.post(url, headers=headers, json=payload)
print(response.json())
```

---

## 3. 日常核心功能与自然语言调用范例

注册成功后，你**完全不需要打开任何黑窗口终端**，直接在聊天对话中指示大模型即可：

### 3.1 硬件与系统健康巡检
- **提问**：“帮我巡检一下绿联 NAS 的当前状态、CPU 温度、内存占用和风扇转速。”
- **大模型调用**：`nas_system_get_overview`, `nas_system_get_thermal`

### 3.2 33TB 存储池、SMART 与 SSD 缓存
- **提问**：“我的 4 块希捷机械盘和 2 块西数 NVMe 固态健康度如何？有没有坏道？另外查看一下 bcache SSD 缓存的命中率。”
- **大模型调用**：`nas_storage_get_disks_smart`, `nas_storage_get_bcache_stats`, `nas_storage_get_volume_usage`

### 3.3 Docker 生产容器与日志排障
- **提问**：“检查当前所有 Docker 容器状态，特别是 MoviePilot 和 Emby，把 MoviePilot 最近的错误日志提取出来排查。”
- **大模型调用**：`nas_docker_list_containers`, `nas_docker_get_logs`

### 3.4 影视中心与 TMDB 智能刮削
- **提问**：“刚下了新电影，帮我触发一次影视中心 TMDB 智能刮削；另外某部电影被识别错了，帮我重刮为流浪地球2。”
- **大模型调用**：`nas_video_trigger_scrape`, `nas_video_rematch_title`

### 3.5 核心扩展能力：“适配应用” (全自动现场生成 Tool)
- **提问**：“我刚刚安装了一个新应用 `com.ugreen.xunlei`，帮我现场适配它。”
- **大模型调用**：`nas_ugos_adapt_app({"target_app": "com.ugreen.xunlei"})`
- **结果**：服务会在后台自动完成端口、配置与 systemd 单元自省，即刻动态生成并挂载该应用的专属 Tool 集，无需重启服务！

### 3.6 高危操作二次确认体验
- **对话示例**：
  - 用户：“帮我把 MoviePilot 容器重启一下。”
  - 大模型：“⚠️ **高危操作安全拦截**：即将重启 Docker 容器 [moviepilot]，该操作将导致下载或自动化服务中断 5~30 秒。是否确认执行？”
  - 用户：“确认。”
  - 大模型：“已携带安全票据执行完成平滑重启，并记录审计日志。”

---

## 4. 服务端部署规范与维护指引 (备查)

服务端已完成自动化部署并常驻运行，日常无需关心。若未来更换配置或升级版本，可参考以下配置路径：

| 资源项目 | NAS 路径 | 说明 |
| :--- | :--- | :--- |
| **可执行二进制** | `/usr/local/bin/ugreen-nas-mcp` | 自包含单二进制文件，无外部运行时依赖 |
| **配置文件** | `/etc/ugreen-nas-mcp/config.json` | 端口、只读开关、API Key 等配置 |
| **systemd 单元** | `/etc/systemd/system/ugreen-nas-mcp.service` | 开机自启服务配置 |
| **安全审计日志** | `/var/log/ugreen-nas-mcp/audit.log` | 记录所有经用户确认执行的高危操作 |

### 常用后台维护指令 (备用)
- 查看服务运行状态：`systemctl status ugreen-nas-mcp`
- 重启服务：`systemctl restart ugreen-nas-mcp`
- 查看实时运行日志：`journalctl -u ugreen-nas-mcp -f`

# 绿联 NAS (UGOS Pro) 专属 MCP 服务端 (ugreen-nas-mcp)

一套专为绿联 NAS (UGOS Pro) 打造的标准 Model Context Protocol (MCP) 服务端。
让大模型（Claude、ChatGPT、Cursor、Codex 等）能够安全、受控、深度、全方位地管理 NAS 硬件健康、存储阵列、Docker 容器与全域 44 个官方原生应用。

> 核心设计哲学：日常完全免开 SSH 终端。
> 仅需在初始部署时使用一次 SSH 进行安装，之后服务作为 systemd 守护进程常驻后台并开机自启。你只需在 AI 客户端中与大模型自然语言对话，即可完成全盘管理与智能运维。

---

## 目录

- [一行命令快速安装 (推荐)](#-一行命令快速安装-推荐)
- [核心功能亮点](#-核心功能亮点)
- [安全防护与二阶段确认机制](#-安全防护与二阶段确认机制)
- [客户端接入配置 (Cursor / Claude / VS Code)](#-客户端接入配置)
- [常用自然语言操控范例](#-常用自然语言操控范例)
- [核心 Tools 清单概览](#-核心-tools-清单概览)
- [手动分步安装指南 (备选)](#-手动分步安装指南-备选)

---

## 一行命令快速安装 (推荐)

在绿联 NAS 终端中直接粘贴执行以下**一行命令**，全自动完成下载、配置、注册开机自启并现场生成专属 Key：

```bash
curl -fsSL https://raw.githubusercontent.com/EricFL1998/ugreen-mcp/main/install.sh | sudo bash
```

> 提示：国内网络备用加速（如无法直连 GitHub）：
> ```bash
> curl -fsSL https://ghproxy.net/https://raw.githubusercontent.com/EricFL1998/ugreen-mcp/main/install.sh | sudo bash
> ```

安装完成后，终端会**当场自动打印出专属于你的客户端注册 JSON 配置**（包含自动探测到的 NAS 局域网 IP 与现场生成的加密 Key）。之后直接退出终端，日常管理完全免开 SSH！

---

## 核心功能亮点

1. **日常彻底告别终端黑窗口**：
   - 服务基于轻量 Go 静态编译为单二进制文件（无外部运行依赖，内存占用仅约 2.3MB）。
   - 随 NAS 开机自启，崩溃秒级自动拉起，通过局域网 HTTP/SSE 长连接暴露服务。
2. **全系统 44 个官方应用深度覆盖**：
   - **影视中心**：触发 TMDB 智能海报墙刮削、影片识别错误强制重匹配、未识别视频命名排查。
   - **相册与本地 AI**：相册人脸识别/场景识别开关、AI Console 本地模型调度、监控中心摄像头状态与录像覆盖策略。
   - **下载与传输**：迅雷下载任务创建、全局上传/下载限速、并发数调节；qBittorrent 做种监控；百度网盘等云盘同步排队。
   - **办公与协作**：在线 Office 协同引擎状态、绿联笔记统计、文件历史修改版本追踪与一键回退还原。
3. **混合存储阵列与硬件深度可观测性**：
   - 实时采集全部机械盘与 NVMe 固态硬盘的 SMART 寿命评估、重映射坏道与盘体温度。
   - 监控 md1 (RAID 5) 与 md2 (RAID 1) 阵列在线健康状况与掉盘预警。
   - 监控 bcache NVMe SSD 高速缓存池读写命中率与待刷入脏数据。
   - 查询 33TB Btrfs 存储池的单副本数据、双副本元数据占用与各子卷空间。
4. **生产级 Docker 容器集群运维**：
   - 深度兼容已运行的 15+ 容器（MoviePilot、Emby、HomeAssistant、qBittorrent 等）。
   - 查看健康状态、端口映射、实时提取 Tail 排错日志。
5. **“适配应用”动态自省扩展引擎 (nas_ugos_adapt_app)**：
   - 后续在 NAS 上安装任意新套件或新容器，只需对大模型说一句“帮我适配一下刚装的应用”，服务会自动完成自省探测并在当前会话中**现场动态生成专属 Tool 集**，协议热重载，无需重启后台服务！

---

## 安全防护与二阶段确认机制

为避免大模型由于幻觉或误触引发不可逆灾难，本服务构建了三道严格的安全防线：

1. **动态加密 Authorization Key**：
   - 服务安装时通过安全密码学随机算法现场动态生成专属 Token，杜绝弱口令与硬编码。
2. **二阶段高危拦截确认协议 (Two-Phase Confirmation)**：
   - 涉及永久删除文件、重启生产容器、重启核心套件等操作被标记为 **L3 级高危动作**。
   - 首次调用强制拦截，生成短效 60 秒 SHA256 票据 (confirm_token)，并返回不可逆影响评估。
   - 只有大模型向用户展示警告，用户在聊天中明确回复“确认”后，模型携带票据再次发起，系统才会真正执行，并记录到本地审计日志 (/var/log/ugreen-nas-mcp/audit.log)。
3. **数据卷白名单沙箱**：
   - 文件操作严格锁定在用户数据存储卷范围（如 /volume1/），强制屏蔽底座私有目录（如 @appstore 内部配置）以及系统根目录（/etc、/proc 等）。

---

## 客户端接入配置

将服务端生成的配置填入你常用的大模型客户端：

### 1. Cursor
在 **Settings** -> **Features** -> **MCP Servers** -> **+ Add New MCP Server**，或者在 `~/.cursor/mcp.json` 中添加：
```json
{
  "mcpServers": {
    "ugreen-nas": {
      "url": "http://<nas_ip>:8095/sse",
      "headers": {
        "Authorization": "Bearer <your_generated_key>"
      }
    }
  }
}
```

### 2. Claude Desktop
在 `claude_desktop_config.json`（设置 -> Developer -> Edit Config）中填入：
```json
{
  "mcpServers": {
    "ugreen-nas": {
      "url": "http://<nas_ip>:8095/sse",
      "headers": {
        "Authorization": "Bearer <your_generated_key>"
      }
    }
  }
}
```

### 3. VS Code (Cline / Roo Code / Continue)
在插件的 MCP 设置中填入相同的配置即可。

---

## 常用自然语言操控范例

接入完成后，直接在对话框中和大模型对话：

- **硬件巡检**：
  > “巡检一下绿联 NAS 当前的 CPU 核心温度、风扇转速、总内存使用和系统负载。”
- **存储与硬盘健康**：
  > “查看 4 块机械硬盘和 2 块 NVMe 固态的 SMART 坏道与通电时间，看看 bcache 读写缓存的命中率。”
- **Docker 排障**：
  > “列出当前运行的 16 个容器，查看 MoviePilot 和 Emby 是否健康，把 MoviePilot 最近的错误日志调出来排查。”
- **影视与海报刮削**：
  > “刚下载了新电影，帮我触发一次影视中心 TMDB 智能刮削；另外有部电影识别错了，帮我重刮为流浪地球2。”
- **动态应用适配 (自省扩展)**：
  > “我刚安装了一个新应用 com.ugreen.xunlei，帮我现场适配它。”
  > （模型将调用 nas_ugos_adapt_app，现场探测并动态生成专属 Tool 集，无需重启服务）
- **高危安全确认**：
  > “帮我重启一下 MoviePilot 容器。”
  > （大模型会弹出拦截警告并展示断流风险，确认继续后才会安全执行，并记录到本地审计日志）

---

## 核心 Tools 清单概览

| 模块分类 | MCP Tool 名称 | 安全等级 | 功能概述 |
| :--- | :--- | :--- | :--- |
| **影视中心** | `nas_video_get_libraries` | L1 (Safe) | 查看影视媒体库分类、绑定文件夹与影片总数 |
| | `nas_video_trigger_scrape` | L2 (Medium) | 触发增量/全量扫描，从 TMDB 自动匹配高清海报与元数据 |
| | `nas_video_rematch_title` | L2 (Medium) | 针对错误识别影片，传入正确片名/TMDB ID 强制重刮校准 |
| | `nas_video_get_unmatched_files`| L1 (Safe) | 筛选出未刮削成功的文件列表，智能分析命名格式 |
| **应用中心** | `nas_appstore_search_online`| L1 (Safe) | 在绿联官方市场检索所有可用应用与安装信息 |
| | `nas_ugos_list_apps` | L1 (Safe) | 一览全系统 44 个官方套件的运行状态与资源消耗 |
| | `nas_ugos_get_app_config` | L1 (Safe) | 读取任意自带套件的底层 config.json 业务配置 |
| | `nas_ugos_update_app_config` | **L3 (High)** | **【强制二阶段确认】** 调节套件核心业务参数 |
| | `nas_ugos_restart_app` | **L3 (High)** | **【强制二阶段确认】** 平滑重启官方核心套件自愈故障 |
| | `nas_ugos_adapt_app` | L1 (Safe) | **【动态自省扩展】** 现场自省新应用并动态生成专属 Tool 集 |
| **存储管理** | `nas_storage_get_disks_smart`| L1 (Safe) | 采集 4 机械盘 + 2 NVMe 固态完整 SMART 寿命、坏道与温度 |
| | `nas_storage_get_raid_status`| L1 (Safe) | 查看 md1 (RAID 5) 与 md2 (RAID 1) 阵列在线状态与重建进度 |
| | `nas_storage_get_bcache_stats`| L1 (Safe) | 监测 NVMe 读写缓存池命中率、缓存模式及待刷写脏数据 |
| | `nas_storage_get_volume_usage`| L1 (Safe) | 查看 33TB Btrfs 存储池的单副本数据与双副本元数据占用 |
| | `nas_storage_list_subvolumes`| L1 (Safe) | 列出所有 Btrfs 存储子卷及快照版本清单 |
| **Docker容器**| `nas_docker_list_containers` | L1 (Safe) | 监控当前 15+ 容器的运行状况、端口及健康检查 |
| | `nas_docker_get_logs` | L1 (Safe) | 调取容器 Tail 近期日志排查报错故障 |
| | `nas_docker_restart_container`| **L3 (High)** | **【强制二阶段确认】** 平滑安全重启指定 Docker 容器 |
| **文件与系统**| `nas_file_list` / `search` | L1 (Safe) | 浏览与检索 /volume1 共享媒体及数据目录（带沙箱隔离） |
| | `nas_file_preview` | L1 (Safe) | 预览文件头部内容（默认 100 行） |
| | `nas_file_delete` | **L3 (High)** | **【强制二阶段确认】** 永久删除指定数据文件 |
| | `nas_system_get_overview` | L1 (Safe) | 整机型号、CPU/内存/Swap 负载与开机时间大盘 |
| | `nas_system_get_thermal` | L1 (Safe) | 采集 CPU 核心温度与风扇当前实时转速 RPM |
| | `nas_security_confirm_action`| L1 (Safe) | 专用确认释放工具，传入 confirm_token 执行高危操作 |

---

## 手动分步安装指南 (备选)

如果需要手动分步编译或安装，可参考以下步骤：

### 1. 编译二进制文件
```bash
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../ugreen-nas-mcp-linux-amd64 .
```

### 2. 部署到 NAS
```bash
scp ugreen-nas-mcp-linux-amd64 <user>@<nas_ip>:/tmp/ugreen-nas-mcp
ssh <user>@<nas_ip>
sudo chmod +x /tmp/ugreen-nas-mcp
sudo mv /tmp/ugreen-nas-mcp /usr/local/bin/ugreen-nas-mcp
sudo systemctl enable --now ugreen-nas-mcp.service
/usr/local/bin/ugreen-nas-mcp --show-key
```

---

## 许可证

本项目基于 [MIT License](LICENSE) 开源发布。

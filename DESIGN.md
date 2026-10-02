# 绿联 NAS (UGOS Pro) 专属全系统应用深度适配 MCP 综合设计规范

--- 

## 1. 项目定位与核心设计哲学

- **项目核心目标**：为绿联 UGOS Pro NAS 设备量身定制一套功能完备的 Model Context Protocol (MCP) 服务端，让大模型（Claude、ChatGPT、Cursor、Codex 等）能够安全、受控、深度、全方位地管理 NAS 的硬件、存储阵列、全域 44 个官方原生应用、Docker 容器与网络。
- **免 SSH 终极体验（设计第一原则）**：
  - **安装阶段**：仅在最初部署时使用一次 SSH 完成二进制传输、目录权限配置与 systemd 自启动注册。
  - **日常使用**：彻底摆脱 SSH 终端与黑窗口。MCP Server 在 NAS 端以 systemd 守护进程常驻运行，通过局域网 HTTP/SSE (Server-Sent Events) 长连接协议暴露标准接口（端口 8095），全流程依靠大语言模型的自然语言交互。
  - **安全保障**：通信链路受 Bearer Token 访问鉴权、数据卷白名单沙箱与高危操作二阶段确认机制三重防护。

--- 

## 2. 实测实机架构透视 (机型：<your_nas_hostname>)

通过 SSH 对实机的底层深度探测，提取出的真实软硬件指纹与技术栈如下：

### 2.1 硬件与系统核心配置
- **设备主机名**：<your_nas_hostname>
- **CPU 规格**：Intel Celeron N5105 @ 2.00GHz (4 核 4 线程，集成 Intel UHD 核显，支持 QSV 硬件转码加速)
- **内存与交换分区**：16GB DDR4 物理内存 + 10GB Swap 虚拟内存
- **操作系统底座**：Debian GNU/Linux 12 (bookworm)，定制 Linux 内核 6.18.15，原生集成 systemd

### 2.2 存储池与底层硬盘阵列拓扑
- **物理磁盘分布**：
  - 4 x 16TB 希捷酷狼 (Seagate IronWolf, ST16000VN001, SATA 6.0Gb/s)：/dev/sda, /dev/sdb, /dev/sdc, /dev/sdd
  - 2 x 500GB 西数蓝盘 NVMe 固态 (WD Blue SN570, PCIe Gen3 NVMe)：/dev/nvme0, /dev/nvme1
- **RAID 阵列层 (mdadm)**：
  - /dev/md1: RAID 5 阵列，由 4 块机械硬盘分区组合而成，净可用容量约 33TB，当前状态为满盘在线 [UUUU]。
  - /dev/md2: RAID 1 镜像阵列，由 2 块 NVMe 固态分区组合而成，净容量 487GB [UU]，作为高速读写缓存池。
- **混合缓存加速层 (bcache)**：
  - 底层将 md2 (NVMe RAID1) 挂载为高速缓存盘，md1 (HDD RAID5) 挂载为后端存储盘，聚合成虚拟块设备 /dev/bcache0。
- **文件系统与子卷层 (Btrfs)**：
  - /dev/bcache0 格式化为 Btrfs 文件系统并挂载至主存储卷 /volume1（总容量 33TB，当前已用 1.1TB，空闲 32TB）。
  - Btrfs 核心子卷结构：Video (ID 256), Docker (ID 257), Agent (ID 258), Music (ID 259), @home 用户主目录 (ID 261), @appstore 官方应用持久化目录。

### 2.3 Docker 与容器运行时
- **Docker 版本**：Docker Engine Community 29.6.2，Unix 套接字：/var/run/docker.sock
- **运行中生产容器 (15+)**：moviepilot, amilys_embyserver, qbittorrent-app-1, homeassistant, BookLore, BookLore-DB, vaultwarden, portainer, gitea, flare, go2rtc, lucky, trading-grid, relay-autoheal, binance-xray, hermes。

### 2.4 原生应用与套件生态 (44 个核心组件)
- 绿联 UGOS Pro 应用统一安装于 /volume1/@appstore/com.ugreen.*, 并由 systemd 注册为 *_serv.service 独立守护进程。

--- 

## 3. 高危操作防误触与二阶段确认机制 (Two-Phase Confirmation Protocol)

在大模型管理真实 NAS 设备的场景下，模型意图漂移或用户口误可能导致重大数据丢失或服务中断。本服务设计了严格的二阶段确认工作流与操作审计机制。

### 3.1 操作风险分级矩阵 (Risk Classification)

| 等级 | 风险类别 | 判定标准 | 涵盖典型操作 | 处理策略 |
| :--- | :--- | :--- | :--- | :--- |
| L1 (Safe) | 只读巡检 | 无任何副作用的查询、监控、指标调阅 | 硬盘 SMART 检测、CPU/风扇温度、容器列表、影视库查询、在线应用搜索、日志调阅 | 直接执行，无阻碍 |
| L2 (Medium)| 温和变更 | 可逆或低影响的业务参数调整、单任务控制 | 添加迅雷下载任务、触发影视 TMDB 增量刮削、修改相册 AI 识别开关、设置限速 | 只读开关放行后直接执行 |
| L3 (High) | 高危操作 | 不可逆数据破坏、关键生产服务中断、系统级配置覆写 | 文件永久删除、生产容器停止/销毁、原生核心套件重启/卸载、RAID 重组、快照回滚、恢复出厂设置 | 强制进入二阶段令牌确认机制 |

### 3.2 二阶段确认交互工作流

1. 大模型首次调用高危 Tool (如 nas_file_delete 或 nas_docker_restart)，系统拒绝执行。
2. 系统生成带有 SHA256 指纹的操作票据 (confirm_token)，有效期 60 秒。
3. 系统返回包含【风险等级】、【受影响对象】、【不可逆后果评估】及【用户确认提示词】的警告报文。
4. 大模型向用户展示警告，向用户明确申请许可。
5. 用户明确答复“确认”后，大模型携带 confirm_token 再次发起执行，系统校验通过并记入审计日志，随后立即销毁 token。

### 3.3 审计日志追溯
所有确认执行的高危操作均会写入 /var/log/ugreen-nas-mcp/audit.log，记录时间、请求参数、客户端 IP 与影响范围。

--- 

## 4. 全系统 44 个官方应用深度适配与功能细则

### 集群一：影音多媒体集群 (Media & Entertainment - 8个组件)
- 涵盖：videomgr (影视中心), music (音乐中心), comic (漫画), ebookmgr (电子书), player (播放器), dlna (媒体投屏), transcode (视频转码), mediaserver (流媒体底座)。
- 核心功能：
  1. nas_video_get_libraries: 查询所有影视库（电影/电视剧/动漫）的分类、绑定的存储目录与影片数量。
  2. nas_video_trigger_scrape: 启动增量/全量扫描，自动连线 TMDB 匹配高清海报、演职员表、中文简介、评分与剧集分集。
  3. nas_video_rematch_title: 强制修正错误识别的电影或电视剧海报墙与元数据。
  4. nas_video_get_scrape_tasks: 调阅当前后台是否有正在运行的刮削任务、处理进度百分比以及与 TMDB 的网络连通状况。
  5. nas_video_get_unmatched_files: 调出所有未刮削成功的视频清单，分析命名规范并给出重命名建议。
  6. nas_video_get_settings / update_settings: 调阅与修改影视设置（刮削源代理配置、语言偏好、硬件转码加速偏好等）。
  7. nas_music_get_status / trigger_scan: 音乐中心曲目统计与专辑封面/歌词索引扫描。
  8. nas_reader_get_overview / trigger_index: 漫画与电子书库格式与图书总数概览。
  9. nas_transcode_get_status: 查看 Intel QSV 核显硬件转码并发与负载。

### 集群二：相册、本地 AI 与监控集群 (AI & Computer Vision - 4个组件)
- 涵盖：photo (个人相册), aiconsole (本地AI模型中心), thumb/thumbcore (缩略图引擎), cameramgr (监控中心)。
- 核心功能：
  1. nas_photo_get_overview: 相册总数、时间线、待处理 AI 任务概览。
  2. nas_photo_get_ai_settings / update_ai_settings: 调节人脸识别、场景识别、集中推理参数。
  3. nas_photo_trigger_reindex: 针对新放入的照片库触发深度特征提取与海报级缩略图重建。
  4. nas_aiconsole_list_models: 查看本地 AI 神经网络模型清单与显存占用。
  5. nas_camera_list_devices: 查看监控中心接入的摄像头列表与录像占用。

### 集群三：下载传输与网盘集群 (Download & Cloud Sync - 4个组件)
- 涵盖：xunlei (迅雷下载), downloadmgr (官方下载中心), docker.qbittorrent (专属BT下载), netdisk (网盘工具)。
- 核心功能：
  1. nas_xunlei_get_tasks: 查看迅雷当前下载与已完成任务详情。
  2. nas_xunlei_add_task: 传入磁力链或链接创建迅雷下载任务。
  3. nas_xunlei_update_settings: 调节迅雷选项（上传/下载限速、并发数、目录）。
  4. nas_download_get_stats / set_speed_limit: 汇总 qBittorrent 全局做种率与速度限额。
  5. nas_netdisk_list_bindings / get_sync_tasks: 查看百度网盘等绑定账号的同步排队。

### 集群四：存储池、快照与数据安全集群 (Storage & Security - 4个组件)
- 涵盖：storagemgr (存储管理), snapshot (快照中心), vault (私密保险箱), antivirus (安全管家)。
- 核心功能：
  1. nas_storage_get_disks_smart: 4 块希捷 16TB 机械盘 + 2 块西数 NVMe 固态完整 SMART 寿命、坏道与温度。
  2. nas_storage_get_raid_status: 监控 md1 (RAID 5) 与 md2 (RAID 1) 健康状态与降级报警。
  3. nas_storage_get_bcache_stats: NVMe SSD 读写缓存池命中率、缓存模式及待刷写脏数据。
  4. nas_storage_get_volume_usage: 33TB Btrfs 存储池的实时分配与剩余空间。
  5. nas_snapshot_list / create: Btrfs 子卷快照列表查询与即时原子快照生成。
  6. nas_antivirus_get_status / trigger_scan: 病毒库状态与安全扫描。

### 集群五：虚拟化、容器与块存储集群 (Virtualization & Cloud Native - 3个组件)
- 涵盖：docker (官方Docker), kvm (虚拟机管理), iscsi (SAN存储)。
- 核心功能：
  1. nas_docker_list_containers: 监控当前 15+ 容器的健康检查、启动时长与端口映射。
  2. nas_docker_get_logs: 调取容器日志，辅助排查 MoviePilot、Emby 等报错故障。
  3. nas_docker_restart_container: (L3 级高危) 强制二阶段确认后平滑安全重启。
  4. nas_kvm_list_vms / control_vm: KVM 虚拟机列表与电源控制。
  5. nas_iscsi_list_targets: iSCSI 目标与 LUN 映射状态。

### 集群六：协同办公与文档集群 (Office & Collaboration - 4个组件)
- 涵盖：office (在线文档), note (绿联笔记), contact (通讯录), versionmgr (文件版本管理)。
- 核心功能：
  1. nas_version_get_history / restore: 追踪指定文件的修改版本并支持一键还原。
  2. nas_office_get_status: 在线协同文档服务健康状态。
  3. nas_note_get_overview: 个人笔记总数与分类大盘。

### 集群七：系统底座、应用中心与运维集群 (Core System & Store - 17个组件)
- 涵盖：appmgr (应用中心), ctlmgr / ugos_serv (控制面板), taskmgr (任务管理), logmgr (日志中心), network (网络服务), filemgr (文件管理), globalsearch (全局搜索) 等。
- 核心功能：
  1. nas_appstore_search_online: 在官方应用市场检索所有可安装套件、版本与详情。
  2. nas_appstore_install_app: 一键在线下载并静默安装指定官方套件。
  3. nas_appstore_get_install_progress: 查询当前应用下载与安装实时进度。
  4. nas_ugos_list_apps: 一览全系统 44 个应用组件的当前运行状态、PID 与资源消耗。
  5. nas_ugos_get_app_config / update_app_config: 通用配置引擎，调阅和更新任意官方套件 config.json 中的所有业务参数。
  6. nas_ugos_restart_app: (L3 级高危) 重启任意发生异常的白名单套件。
  7. nas_ugos_get_app_logs: 读取任意官方套件的专属 systemd 运行日志。
  8. nas_system_get_thermal: 采集 CPU 各核心温度与风扇当前实时转速 RPM。
  9. nas_system_get_overview: 整机 CPU/内存/Swap/开机时间大盘。
  10. nas_system_get_network: 网卡瞬时吞吐与链路状态。
  11. nas_file_list / search / preview / delete: Btrfs 共享目录安全沙箱检索与文件管理。
  12. nas_security_confirm_action: 专用高危确认释放工具。

--- 

### 4.8 核心扩展能力：“适配应用”动态自省与 Tool 生成引擎 (Dynamic App Adapter)

为了应对绿联未来新增官方套件、第三方安装应用或自定义 Docker 应用，系统内置 **“适配应用”动态生成工具**：

- **MCP Tool 标识名**：`nas_ugos_adapt_app`
- **设计目标**：
  让大模型具备**自我进化与动态扩展能力**。用户只需自然语言发出指令（例如：“帮我适配一下刚装的某某应用”），服务就会全自动对该应用展开软硬件自省，并动态暴露专属的 MCP Tools。
- **动态探测与自省流程**：
  1. **元数据自省 (Metadata Reflection)**：
     扫描 `/volume1/@appstore/<app_id>/config.json` 或指定 Docker 容器的 Label/ENV，提取其应用类别、内部绑定的 Unix Socket 路径、对外暴露的 Web/API 端口、配置文件位置与数据存储目录。
  2. **服务通信与接口探测 (Interface Discovery)**：
     - 若为 UGOS 原生套件：探测其对应的 systemd 单元（`*_serv.service`）、日志文件路径，以及在 `/var/ugreen/<app_id>.sock` 开放的内部通信端点。
     - 若为 Docker 应用：自动检查其暴露的 HTTP API、健康检查指令 (Healthcheck) 与数据挂载卷。
  3. **动态生成对应 MCP Tool Schema**：
     系统运行时动态构建并注册该应用的专属 MCP Tools，包括：
     - `nas_<app>_get_status`: 应用专属状态与健康度检测。
     - `nas_<app>_get_config` / `nas_<app>_set_config`: 专属配置文件参数读写。
     - `nas_<app>_get_logs`: 专属日志调阅与故障诊断。
     - `nas_<app>_control`: 专属启停/重启指令（自动绑定二阶段高危确认机制）。
  4. **热重载无需重启 (Hot-Reloading)**：
     新生成的 Tool 集即时注册至当前正在运行的 MCP 协议上下文中，并通过 MCP `notifications/tools/list_changed` 通知前端 AI 客户端即刻刷新工具列表，无需重启后台服务！

--- 

## 5. 常驻后台服务与免 SSH 落地部署规范

- **安装目录**：/usr/local/bin/ugreen-nas-mcp
- **配置文件**：/etc/ugreen-nas-mcp/config.json
- **systemd 托管服务**：/etc/systemd/system/ugreen-nas-mcp.service
  - 启动参数：--config /etc/ugreen-nas-mcp/config.json --port 8095
  - 开机自启：systemctl enable ugreen-nas-mcp.service
  - 自动拉起：Restart=always, RestartSec=5s
  - 暴露协议：HTTP / SSE，绑定端口 0.0.0.0:8095
- **客户端接入配置 (Cursor / Claude Desktop / Codex)**：
  ```json
  {
    "mcpServers": {
      "ugreen-nas": {
        "url": "http://<nas_ip>:8095/sse",
        "headers": {
          "Authorization": "Bearer <YOUR_GENERATED_API_KEY>"
        }
      }
    }
  }
  ```
- **日常使用方式**：一次性部署完成后，日常绝不需要开启 SSH 终端，直接在客户端自然语言与大模型对话即可完成全盘管理与运维！

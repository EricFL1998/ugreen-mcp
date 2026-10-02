package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"ugreen-nas-mcp/config"
	"ugreen-nas-mcp/protocol"
	"ugreen-nas-mcp/security"
)

func Register(r *protocol.ToolRegistry) {
	r.Register(protocol.Tool{
		Name:        "nas_docker_list_containers",
		Description: "查询 NAS 上所有 Docker 生产容器 (MoviePilot, Emby, qBittorrent, HomeAssistant等) 的运行状态、健康检查与端口映射",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, handleListContainers)

	r.Register(protocol.Tool{
		Name:        "nas_docker_get_logs",
		Description: "调阅指定 Docker 容器的近期运行与报错日志 (Tail 模式，支持行数限定)",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"container": map[string]interface{}{
					"type":        "string",
					"description": "容器名称或 ID，例如 moviepilot 或 amilys_embyserver",
				},
				"tail": map[string]interface{}{
					"type":        "integer",
					"description": "获取倒数多少行日志，默认 100 行",
				},
			},
			"required": []string{"container"},
		},
	}, handleGetLogs)

	r.Register(protocol.Tool{
		Name:        "nas_docker_restart_container",
		Description: "【高危操作 - 二阶段确认】平滑安全重启指定 Docker 生产容器，避免服务长时间中断",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"container": map[string]interface{}{
					"type":        "string",
					"description": "需要重启的容器名称或 ID",
				},
				"confirm_token": map[string]interface{}{
					"type":        "string",
					"description": "若首次调用留空；若为确认执行，传入此前生成的安全票据 (confirm_token)",
				},
			},
			"required": []string{"container"},
		},
	}, handleRestartContainer)
}

func getDockerClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/docker.sock")
			},
		},
	}
}

func handleListContainers(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	client := getDockerClient()
	resp, err := client.Get("http://localhost/containers/json?all=true")
	if err != nil {
		return nil, fmt.Errorf("无法连接 Docker Unix Socket: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawList []map[string]interface{}
	if err := json.Unmarshal(body, &rawList); err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	for _, c := range rawList {
		names, _ := c["Names"].([]interface{})
		nameStr := ""
		if len(names) > 0 {
			nameStr = strings.TrimPrefix(fmt.Sprintf("%v", names[0]), "/")
		}

		results = append(results, map[string]interface{}{
			"id":      c["Id"].(string)[:12],
			"name":    nameStr,
			"image":   c["Image"],
			"status":  c["Status"],
			"state":   c["State"],
			"ports":   c["Ports"],
			"created": c["Created"],
		})
	}

	return results, nil
}

func handleGetLogs(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	container, _ := args["container"].(string)
	if container == "" {
		return nil, fmt.Errorf("必须指定容器名称")
	}

	tail := 100
	if t, ok := args["tail"].(float64); ok && t > 0 {
		tail = int(t)
	}

	client := getDockerClient()
	url := fmt.Sprintf("http://localhost/containers/%s/logs?stdout=true&stderr=true&tail=%d", container, tail)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("调取容器日志失败: %v", err)
	}
	defer resp.Body.Close()

	rawLogs, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	cleanLines := []string{}
	lines := strings.Split(string(rawLogs), "\n")
	for _, l := range lines {
		// Strip docker multiplex header if present (8 bytes)
		if len(l) > 8 && (l[0] == 1 || l[0] == 2) {
			l = l[8:]
		}
		if trimmed := strings.TrimSpace(l); trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	return map[string]interface{}{
		"container": container,
		"line_count": len(cleanLines),
		"logs": cleanLines,
	}, nil
}

func handleRestartContainer(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	container, _ := args["container"].(string)
	if container == "" {
		return nil, fmt.Errorf("必须指定要重启的容器名称")
	}

	cfg := config.GetConfig()
	guardian := security.GetGuardian()

	confirmToken, _ := args["confirm_token"].(string)
	if confirmToken == "" {
		// First phase: Intercept & request confirmation
		summary := fmt.Sprintf("重启 Docker 生产容器 [%s]", container)
		impact := fmt.Sprintf("容器 [%s] 将被短暂停止并重新启动，可能导致关联客户端（如影视播放、下载或自动化任务）断连 5~30 秒", container)
		token, err := guardian.RequestConfirmation("DOCKER_RESTART", container, "", summary, impact)
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"status":             "CONFIRMATION_REQUIRED",
			"confirm_token":      token,
			"expires_in_seconds": 60,
			"risk_level":         "CRITICAL",
			"action_summary":     summary,
			"impact_analysis":    impact,
			"prompt_to_user":     fmt.Sprintf("⚠️ 高危操作安全拦截：即将重启容器 [%s]。如果确认，请回复确认并由模型携带该 Token 提交执行。", container),
		}, nil
	}

	// Second phase: Validate token and execute
	if err := guardian.VerifyAndConsume(confirmToken, "DOCKER_RESTART", container); err != nil {
		return nil, err
	}

	if cfg.ReadOnly {
		return nil, fmt.Errorf("当前服务运行在全局只读模式 (read_only=true)，禁止修改运行状态")
	}

	client := getDockerClient()
	url := fmt.Sprintf("http://localhost/containers/%s/restart?t=10", container)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("重启容器失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Docker 返回错误 (%d): %s", resp.StatusCode, string(b))
	}

	return map[string]string{
		"status":  "SUCCESS",
		"message": fmt.Sprintf("容器 [%s] 已安全完成平滑重启", container),
	}, nil
}

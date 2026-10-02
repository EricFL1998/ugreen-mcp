package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"ugreen-nas-mcp/config"
	"ugreen-nas-mcp/drivers/apps"
	"ugreen-nas-mcp/drivers/docker"
	"ugreen-nas-mcp/drivers/files"
	"ugreen-nas-mcp/drivers/storage"
	"ugreen-nas-mcp/drivers/system"
	"ugreen-nas-mcp/protocol"
)

func main() {
	configPath := flag.String("config", "/etc/ugreen-nas-mcp/config.json", "配置文件路径")
	portFlag := flag.Int("port", 0, "覆盖监听端口")
	printTokenOnly := flag.Bool("show-key", false, "仅显示当前配置的 Authorization Key")
	flag.Parse()

	cfg, isNew, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Printf("[WARN] 加载配置失败，使用默认配置: %v", err)
		cfg = config.DefaultConfig()
		isNew = true
	}
	if *portFlag > 0 {
		cfg.Port = *portFlag
	}

	if *printTokenOnly {
		fmt.Println(cfg.AuthToken)
		return
	}

	fmt.Println("================================================================================")
	fmt.Println("       UGREEN NAS (UGOS Pro) MCP 服务已启动就绪 (开机自启守护进程)")
	fmt.Println("================================================================================")
	fmt.Printf(" [MCP 端点地址] : http://%s:%d/sse\n", cfg.Host, cfg.Port)
	fmt.Println(" --------------------------------------------------------------------------------")
	if isNew {
		fmt.Println(" [全新动态生成的 Authorization Key (注册凭证)]:")
	} else {
		fmt.Println(" [当前生效的 Authorization Key (注册凭证)]:")
	}
	fmt.Printf("    Bearer %s\n", cfg.AuthToken)
	fmt.Println(" --------------------------------------------------------------------------------")
	fmt.Println(" [MCP 客户端注册配置 (Cursor / Claude Desktop / VS Code)]:")
	jsonSample := fmt.Sprintf("{\n  \"mcpServers\": {\n    \"ugreen-nas\": {\n      \"url\": \"http://<nas_ip>:%d/sse\",\n      \"headers\": {\n        \"Authorization\": \"Bearer %s\"\n      }\n    }\n  }\n}", cfg.Port, cfg.AuthToken)
	fmt.Println(jsonSample)
	fmt.Println("================================================================================")

	// 1. 初始化注册表并挂载驱动
	registry := protocol.GetRegistry()
	system.Register(registry)
	storage.Register(registry)
	docker.Register(registry)
	files.Register(registry)
	apps.Register(registry)

	log.Printf("[INIT] 已成功装载并就绪 %d 个核心 MCP Tools", len(registry.ListTools()))

	// 2. 初始化 SSE 服务端
	sseServer := protocol.NewSSEServer(registry)

	// 3. 注册 HTTP 路由
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", sseServer.AuthMiddleware(sseServer.HandleSSE))
	mux.HandleFunc("/message", sseServer.AuthMiddleware(sseServer.HandleMessage))

	// 健康探针
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"status\":\"HEALTHY\",\"service\":\"ugreen-nas-mcp\",\"tools_count\":%d}", len(registry.ListTools()))
	})

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[FATAL] 服务运行失败: %v", err)
		os.Exit(1)
	}
}

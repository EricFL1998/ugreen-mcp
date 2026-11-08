package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ugreen-nas-mcp/config"
)

// setConfigForTest 走真实的 config.LoadConfig（写临时 config.json），
// 不在生产代码里为测试留任何导出入口。
func useAllowed(t *testing.T, allowed ...string) {
	t.Helper()
	payload := map[string]interface{}{
		"port":           8095,
		"host":           "127.0.0.1",
		"read_only":      true,
		"allowed_paths":  allowed,
		"audit_log_path": filepath.Join(t.TempDir(), "audit.log"),
	}
	data, _ := json.Marshal(payload)
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatalf("写临时配置失败: %v", err)
	}
	if _, _, err := config.LoadConfig(p); err != nil {
		t.Fatalf("加载临时配置失败: %v", err)
	}
}

func TestPathSandbox(t *testing.T) {
	useAllowed(t, "/volume1")
	g := GetGuardian()

	cases := []struct {
		path    string
		allowed bool
		why     string
	}{
		{"/volume1", true, "数据卷根目录"},
		{"/volume1/Agent/hermes/.env", true, "本次真实用途：读 .env"},
		{"/volume1/Agent/hermes/grid-trading/data/historical.db", true, "真实库路径"},
		{"/volume1evil/secret.txt", false, "旧实现用 HasPrefix，/volume1evil 会被误放行"},
		{"/etc/shadow", false, "系统路径"},
		{"/volume1/@appstore/x", false, "私有应用目录"},
		{"/volume1/homes/@encrypted@/x", false, "加密目录"},
		{"/tmp/x.db", false, "非数据卷"},
	}
	for _, c := range cases {
		err := g.CheckPathSandbox(c.path)
		if c.allowed && err != nil {
			t.Errorf("应放行但被拦: %s (%s): %v", c.path, c.why, err)
		}
		if !c.allowed && err == nil {
			t.Errorf("应拦截但被放行: %s (%s)", c.path, c.why)
		}
	}
}

func TestCredentialDeny(t *testing.T) {
	useAllowed(t, "/volume1")
	g := GetGuardian()

	denied := []string{
		"/volume1/homes/louxuanchen/.ssh/id_ed25519",
		"/volume1/Agent/hermes/.ssh/authorized_keys",
		"/volume1/Work/server.key",
		"/volume1/Work/cert.pem",
		"/volume1/Work/.gnupg/private-keys-v1.d/x",
		"/volume1/Work/app.shadow",
	}
	for _, p := range denied {
		if err := g.CheckCredentialDeny(p); err == nil {
			t.Errorf("凭据文件应被拦截: %s", p)
		}
	}

	allowed := []string{
		"/volume1/Agent/hermes/.env",
		"/volume1/Agent/hermes/grid-trading/data/pnl_history.jsonl",
		"/volume1/Agent/hermes/grid-trading/data/grid_balance_history.json",
	}
	for _, p := range allowed {
		if err := g.CheckCredentialDeny(p); err != nil {
			t.Errorf("业务文件应放行: %s: %v", p, err)
		}
	}
}

func TestWithinRoot(t *testing.T) {
	if !withinRoot(filepath.Clean("/volume1"), "/volume1") {
		t.Fatal("根目录自身应视为包含")
	}
	if withinRoot("/volume1evil/x", "/volume1") {
		t.Fatal("/volume1evil 不应被 /volume1 包含")
	}
	if !withinRoot("/volume1/a/b", "/volume1") {
		t.Fatal("子路径应被包含")
	}
}

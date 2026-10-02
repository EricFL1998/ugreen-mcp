package security

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ugreen-nas-mcp/config"
)

type PendingAction struct {
	Token          string    `json:"confirm_token"`
	ActionType     string    `json:"action_type"`
	Target         string    `json:"target"`
	Payload        string    `json:"payload"`
	ActionSummary  string    `json:"action_summary"`
	ImpactAnalysis string    `json:"impact_analysis"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type Guardian struct {
	mu      sync.Mutex
	pending map[string]*PendingAction
}

var (
	defaultGuardian *Guardian
	once            sync.Once
)

func GetGuardian() *Guardian {
	once.Do(func() {
		defaultGuardian = &Guardian{
			pending: make(map[string]*PendingAction),
		}
	})
	return defaultGuardian
}

// RequestConfirmation intercepts an L3 operation and creates a short-lived token
func (g *Guardian) RequestConfirmation(actionType, target, payload, summary, impact string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clean expired tokens
	now := time.Now()
	for k, v := range g.pending {
		if now.After(v.ExpiresAt) {
			delete(g.pending, k)
		}
	}

	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := "act_" + hex.EncodeToString(bytes)

	action := &PendingAction{
		Token:          token,
		ActionType:     actionType,
		Target:         target,
		Payload:        payload,
		ActionSummary:  summary,
		ImpactAnalysis: impact,
		CreatedAt:      now,
		ExpiresAt:      now.Add(60 * time.Second),
	}

	g.pending[token] = action
	return token, nil
}

// VerifyAndConsume checks if the provided confirm_token is valid and consumes it immediately
func (g *Guardian) VerifyAndConsume(token, actionType, target string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	action, exists := g.pending[token]
	if !exists {
		return fmt.Errorf("无效或已过期的确认令牌 (Token)，高危操作已被阻止")
	}

	if time.Now().After(action.ExpiresAt) {
		delete(g.pending, token)
		return fmt.Errorf("确认令牌 (Token) 已过期 (超过60秒)，请重新发起申请")
	}

	if action.ActionType != actionType || action.Target != target {
		delete(g.pending, token)
		return fmt.Errorf("操作参数与初始申请不匹配，安全拦截")
	}

	delete(g.pending, token)
	g.LogAudit(fmt.Sprintf("CONFIRMED_EXECUTION: Type=%s Target=%s Summary=%s", action.ActionType, action.Target, action.ActionSummary))
	return nil
}

// CheckPathSandbox verifies that path is strictly under allowed data volumes (e.g. /volume1)
// and rejects forbidden system directories (@appstore private db, /etc, /proc, etc.)
func (g *Guardian) CheckPathSandbox(path string) error {
	cleanPath := filepath.Clean(path)
	cfg := config.GetConfig()

	allowed := false
	for _, p := range cfg.AllowedPaths {
		if strings.HasPrefix(cleanPath, p) {
			allowed = true
			break
		}
	}

	if !allowed {
		return fmt.Errorf("路径安全沙箱拦截：仅允许操作数据卷路径（如 /volume1/），禁止访问底层系统路径 %s", cleanPath)
	}

	// Forbid dangerous private directories
	forbiddenSegments := []string{"@appstore", "@cameramgr", "@encrypted@", "/etc", "/proc", "/sys"}
	for _, seg := range forbiddenSegments {
		if strings.Contains(cleanPath, seg) {
			return fmt.Errorf("路径安全沙箱拦截：禁止直接读写 NAS 系统核心或加密私有目录 %s", cleanPath)
		}
	}

	return nil
}

// LogAudit writes approved or executed high-risk actions to audit log
func (g *Guardian) LogAudit(entry string) {
	cfg := config.GetConfig()
	logPath := cfg.AuditLogPath
	if logPath == "" {
		logPath = "/var/log/ugreen-nas-mcp/audit.log"
	}

	dir := filepath.Dir(logPath)
	_ = os.MkdirAll(dir, 0755)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), entry)
	_, _ = f.WriteString(line)
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"stage-rigging-clearance/internal/application"
	"stage-rigging-clearance/internal/httpui"
	"stage-rigging-clearance/internal/storage"
)

const defaultAddr = "127.0.0.1:19081"

type config struct {
	addr      string
	database  string
	selfcheck bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("stageclearance: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := parseConfig(args, os.Getenv("PORT"))
	if err != nil {
		return err
	}
	database := cfg.database
	if cfg.selfcheck && database == "stage-clearance.db" {
		database = "file:selfcheck?mode=memory&cache=shared"
	}
	store, err := storage.Open(database)
	if err != nil {
		return err
	}
	defer store.Close()
	service := application.New(store)
	handler := httpui.New(service)
	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", cfg.addr, err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		} else {
			serveErr <- nil
		}
	}()
	actualAddr := listener.Addr().String()
	log.Printf("舞台吊挂安全核验服务监听 http://%s", actualAddr)
	if cfg.selfcheck {
		checkErr := runSelfcheck(actualAddr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(ctx)
		serveResult := <-serveErr
		if checkErr != nil {
			return checkErr
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		if serveResult != nil {
			return serveResult
		}
		log.Printf("selfcheck 完成：完整业务流程与凭据链验证通过")
		return nil
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		log.Printf("收到 %s，开始关闭", sig)
	case err := <-serveErr:
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func parseConfig(args []string, port string) (config, error) {
	set := flag.NewFlagSet("stageclearance", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	var cfg config
	set.StringVar(&cfg.addr, "addr", "", "监听地址，例如 127.0.0.1:19081")
	set.StringVar(&cfg.database, "db", "stage-clearance.db", "SQLite 数据库文件")
	set.BoolVar(&cfg.selfcheck, "selfcheck", false, "运行有界 HTTP 业务自检后退出")
	if err := set.Parse(args); err != nil {
		return cfg, err
	}
	if set.NArg() != 0 {
		return cfg, fmt.Errorf("不支持的位置参数：%s", strings.Join(set.Args(), " "))
	}
	if cfg.addr == "" {
		if strings.TrimSpace(port) != "" {
			p, err := strconv.Atoi(port)
			if err != nil || p < 1 || p > 65535 {
				return cfg, fmt.Errorf("PORT 必须是 1 至 65535 的端口号")
			}
			cfg.addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
		} else {
			cfg.addr = defaultAddr
		}
	}
	host, portText, err := net.SplitHostPort(cfg.addr)
	if err != nil {
		return cfg, fmt.Errorf("-addr 格式无效：%w", err)
	}
	p, err := strconv.Atoi(portText)
	if err != nil || p < 1 || p > 65535 {
		return cfg, fmt.Errorf("-addr 端口必须是 1 至 65535")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return cfg, fmt.Errorf("-addr 必须使用回环 IP，当前为 %q", host)
	}
	return cfg, nil
}

type checkClient struct {
	base   string
	client *http.Client
}

func (c checkClient) call(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s %s 返回 %d: %s", method, path, res.StatusCode, string(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func runSelfcheck(addr string) error {
	c := checkClient{base: "http://" + addr, client: &http.Client{Timeout: 8 * time.Second}}
	if err := c.call("GET", "/healthz", nil, &map[string]any{}); err != nil {
		return fmt.Errorf("健康检查失败: %w", err)
	}
	meta := func(version int64, key, actor, role string) map[string]any {
		return map[string]any{"expectedVersion": version, "idempotencyKey": key, "actor": actor, "role": role}
	}
	create := meta(0, "selfcheck-create", "林舞监", "supervisor")
	create["productionName"] = "自检演出《升降之间》"
	create["venue"] = "实验剧场"
	create["scheduledAt"] = time.Now().Add(2 * time.Hour).UTC()
	create["supervisorName"] = "林舞监"
	var session struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	if err := c.call("POST", "/api/sessions", create, &session); err != nil {
		return err
	}
	if session.Version != 1 {
		return fmt.Errorf("新建批次版本异常")
	}
	itemBody := meta(1, "selfcheck-item", "林舞监", "supervisor")
	itemBody["itemType"] = "bar"
	itemBody["label"] = "主景吊杆 A"
	itemBody["location"] = "舞台中线 4 号位"
	itemBody["ratedLoadKg"] = 1200
	itemBody["plannedLoadKg"] = 640
	itemBody["inspectionStandard"] = "剧场吊挂作业规程 6.2"
	var item struct {
		ID string `json:"id"`
	}
	if err := c.call("POST", "/api/sessions/"+session.ID+"/items", itemBody, &item); err != nil {
		return err
	}
	version := int64(2)
	var hazardID string
	checks := []string{"lock", "wear", "fastening", "clearance", "load"}
	for index, check := range checks {
		body := meta(version, fmt.Sprintf("selfcheck-inspection-%d", index), "周技师", "technician")
		body["itemId"] = item.ID
		body["checkType"] = check
		body["measuredValue"] = "现场值正常"
		body["inspectorName"] = "周技师"
		body["verdict"] = "pass"
		if check == "lock" || check == "load" {
			body["evidenceRef"] = "evidence://selfcheck/" + check
		}
		if check == "wear" {
			body["verdict"] = "fail"
			body["severity"] = "blocking"
			body["scope"] = "主景吊杆钢索端部"
			body["requiredAction"] = "清洁并重新检查磨损痕迹"
			body["assignee"] = "周技师"
			body["dueAt"] = time.Now().Add(time.Hour).UTC()
		}
		var result struct {
			Hazard *struct {
				ID string `json:"id"`
			} `json:"hazard"`
		}
		if err := c.call("POST", "/api/sessions/"+session.ID+"/inspections", body, &result); err != nil {
			return err
		}
		if result.Hazard != nil {
			hazardID = result.Hazard.ID
		}
		version++
	}
	if hazardID == "" {
		return fmt.Errorf("自检未生成阻断隐患")
	}
	remediation := meta(version, "selfcheck-remediation", "周技师", "technician")
	remediation["note"] = "完成清洁并确认痕迹为表面污渍，设备参数无需修订"
	remediation["evidenceRef"] = "evidence://selfcheck/remediation"
	remediation["reviseItem"] = false
	if err := c.call("POST", "/api/sessions/"+session.ID+"/hazards/"+hazardID+"/remediation", remediation, &map[string]any{}); err != nil {
		return err
	}
	version++
	reinspect := meta(version, "selfcheck-reinspect", "赵技师", "technician")
	reinspect["measuredValue"] = "清洁后无可见磨损"
	reinspect["verdict"] = "pass"
	reinspect["evidenceRef"] = "evidence://selfcheck/reinspection"
	reinspect["inspectorName"] = "赵技师"
	if err := c.call("POST", "/api/sessions/"+session.ID+"/hazards/"+hazardID+"/reinspection", reinspect, &map[string]any{}); err != nil {
		return err
	}
	version++
	previewBody := meta(version, "selfcheck-preview", "陈复核", "reviewer")
	var preview struct {
		ReviewToken string `json:"reviewToken"`
	}
	if err := c.call("POST", "/api/sessions/"+session.ID+"/freeze-preview", previewBody, &preview); err != nil {
		return err
	}
	if preview.ReviewToken == "" {
		return fmt.Errorf("冻结预演未签发复核确认令牌")
	}
	freeze := meta(version, "selfcheck-freeze", "陈复核", "reviewer")
	freeze["reviewNote"] = "载荷安全、检验覆盖完整、阻断隐患已闭环"
	freeze["reviewToken"] = preview.ReviewToken
	freeze["confirmed"] = true
	freeze["confirmations"] = map[string]bool{"items": true, "inspections": true, "hazards": true, "loads": true}
	if err := c.call("POST", "/api/sessions/"+session.ID+"/freeze", freeze, &map[string]any{}); err != nil {
		return err
	}
	version++
	issue := meta(version, "selfcheck-issue", "陈复核", "reviewer")
	issue["approvedBy"] = "陈复核"
	if err := c.call("POST", "/api/sessions/"+session.ID+"/certificates", issue, &map[string]any{}); err != nil {
		return err
	}
	var workbench struct {
		Snapshot struct {
			Session struct {
				Status string `json:"status"`
			} `json:"session"`
			Certificates []any `json:"certificates"`
		} `json:"snapshot"`
		Verification struct {
			Valid bool `json:"valid"`
		} `json:"verification"`
	}
	if err := c.call("GET", "/api/sessions/"+session.ID, nil, &workbench); err != nil {
		return err
	}
	if workbench.Snapshot.Session.Status != "released" || len(workbench.Snapshot.Certificates) != 1 || !workbench.Verification.Valid {
		return fmt.Errorf("最终放行状态或凭据完整性验证失败")
	}
	return nil
}

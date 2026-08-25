# BENZHI_README

基于 Go 实现的stage-rigging-clearance Web 项目，一款后端服务，已完整实现面向剧院舞台吊挂系统的演出前安全核验工作台，覆盖批次与构件建档、载荷校验、逐项检验、分级隐患、整改修订、针对性复检、原子冻结、摘要链放行凭据及审计查询，并提供真实 HTTP 有界自检。

## 项目说明
- 项目：benzhi-project-10e6df9d-effa-47eb-ba37-01841fff7572
- 项目用途：已完整实现面向剧院舞台吊挂系统的演出前安全核验工作台，覆盖批次与构件建档、载荷校验、逐项检验、分级隐患、整改修订、针对性复检、原子冻结、摘要链放行凭据及审计查询，并提供真实 HTTP 有界自检。
- Go 工具链：`golang:1.24.0`
- 前端工具链：原生 HTML、CSS 和 JavaScript，由 Go 服务直接提供

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/stageclearance -addr=127.0.0.1:19081 -selfcheck
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-10e6df9d-effa-47eb-ba37-01841fff7572-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-10e6df9d-effa-47eb-ba37-01841fff7572-arm64 linux/arm64
docker run -it benzhi-project-10e6df9d-effa-47eb-ba37-01841fff7572-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/stageclearance -addr=127.0.0.1:19081 -selfcheck`

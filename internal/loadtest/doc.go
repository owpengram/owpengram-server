// Package loadtest 承载 message 模块的压测基线。
//
// 这里只放 env-gated 的压测用例（真实 PostgreSQL），不被生产代码 import。
// 目标与基线方法见 docs/message-module.md 的「Load Baseline」一节。
//
// 运行方式（PowerShell）：
//
//	$env:TELESRV_TEST_POSTGRES_DSN = "postgres://telesrv:telesrv@localhost:5432/telesrv?sslmode=disable"
//	go test ./internal/loadtest/ -run TestMessageSendBaseline -v -count=1
//
// 未设置上述环境变量时用例直接 Skip，因此对默认 `go test ./...` 无副作用。
package loadtest

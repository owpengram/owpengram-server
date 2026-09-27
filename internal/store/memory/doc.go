// Package memory 提供 store 各接口的单进程内存实现：既是测试替身，也是
// 生产环境里缓存、限流、计数器分配、CAS 状态与非持久推送的实际实现
// （服务器单进程运行，无需 Redis 之类的跨进程协调）。
//
// 与 store/postgres 对称：store 主包只定义接口与 DTO，两种后端实现各自
// 独立成包。
package memory

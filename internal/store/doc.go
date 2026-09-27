// Package store 定义存储接口与协议层 DTO，不含具体实现。
//
// 布局：主包只放接口（AuthKeyStore / UserStore / AuthorizationStore /
// CodeStore / UpdateStateStore / UpdateEventStore 等）与协议 DTO；两种后端实现各自独立成对称子包：
//   - store/memory     —— 单进程内存实现（互斥锁/map），生产环境的缓存、限流、
//     计数器分配、CAS 状态与非持久推送均由此提供；服务器本就单进程运行，
//     无需 Redis 这类跨进程协调
//   - store/postgres   —— PostgreSQL（pgx + sqlc 生成查询 + golang-migrate 迁移）
//
// 类型边界：接口签名分两类——
//   - 协议产物用 store 自有 DTO：AuthKeyData、PhoneCode（不依赖 tg.*，也非业务实体）；
//   - 业务实体直接用 domain：UserStore / AuthorizationStore / MessageStore / UpdateEventStore
//     收发 domain.User / domain.Authorization / domain.Message / domain.UpdateEvent。
package store

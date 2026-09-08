// Package redisstore 用 Redis 实现高频、易失且可重建的短状态、缓存、计数器与限流。
//
// 职责边界见 docs/persistence-layer.md §1：Redis 存「态与计数」，丢失可由 PG/协议恢复。
package redisstore

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultOpenPingTimeout = 5 * time.Second

// Open 按地址建立 Redis 连接并用有界 PING 验证；返回的客户端继续保留 go-redis 默认重试策略。
func Open(ctx context.Context, addr, password string, db int) (*redis.Client, error) {
	return open(ctx, addr, password, db, defaultOpenPingTimeout)
}

func open(ctx context.Context, addr, password string, db int, pingTimeout time.Duration) (*redis.Client, error) {
	c := redis.NewClient(&redis.Options{
		Addr:                  addr,
		Password:              password,
		DB:                    db,
		ContextTimeoutEnabled: true,
	})
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping %s: %w", addr, err)
	}
	return c, nil
}

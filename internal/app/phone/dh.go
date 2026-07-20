// Package phone 实现私聊 1:1 通话的信令状态机与 DH 参数下发。
//
// 服务端职责边界：信令转发、状态机、commit-reveal 核验（SHA256(g_a)==g_a_hash）、
// connections 下发。媒体面由客户端 tgcalls 走 P2P/TURN，密钥交换是 E2E 的，
// 服务端不知道也无法验证共享密钥本身。
package phone

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// DHConfigVersion 是 messages.getDhConfig 的静态版本号。p/g 是编译期常量，
// 客户端缓存命中（请求 version 相同）时只回 dhConfigNotModified{random}。
//
// ⚠ 提高此值会让所有客户端在下一次 messages.getDhConfig（每次拨打/接听通话都会调用）
// 强制重新拉取 p/g，而不是信任本地缓存。用于失效任何账号本地可能缓存的陈旧/错误
// p/g（例如账号早年间对接过其它后端、缓存版本号恰好等于当时的 DHConfigVersion，
// 此后再也不会刷新——版本号是纯常量，服务端自己永远不会主动使旧缓存过期）。
// 本次从 1→2 是为诊断一例「A 拨 B 接通即断（key fingerprint/Ga hash 不合）」而提升，
// 与本次通话 bug 排查同批次的服务端改动一起看。
const DHConfigVersion = 2

// DHG 是 DH generator。与官方一致取 3：TDesktop MTP::IsPrimeAndGood 对
// 「官方 2048-bit prime + g∈{3,4,5,7}」有白名单快速通过路径，DrKLO native 同。
const DHG = 3

// dhPrimeHex 是官方 2048-bit safe prime。与 internal/app/account/srp.go 的
// baseP 同值（SRP 与通话 DH 共用官方参数）；改动任一处须同步另一处。
const dhPrimeHex = "c71caeb9c6b1c9048e6c522f70f13f73980d40238e3e21c14934d037563d930f48198a0aa7c14058229493d22530f4dbfa336f6e0ac925139543aed44cce7c3720fd51f69458705ac68cd4fe6b6b13abdc9746512969328454f18faf8c595f642477fe96bb2a941d5bcd1d4ac8cc49880708fa9b378e3c4f3a9060bee67cf9a4a4a695811051907e162753b56b0f6b410dba74d8a84b2a14b3144e0ef1284754fd17ed950d5965b4b9dd46582db1178d169c6bc465b0d6ff9ca3928fef5b9ae4e418fc15e83ebea0f87fa9ff5eed70050ded2849f47bf959d956850ce929851f0d8115f635b105ee2e4e15d04b2454bf6f4fadf034b10403119cd8e3b92fcc5b"

var dhPrime = mustDecodeHex(dhPrimeHex)

// maxDHRandomLength 钳制客户端请求的随机字节数，防御恶意大请求。
const maxDHRandomLength = 1024

// DHPrime 返回官方 2048-bit prime 的拷贝。
func DHPrime() []byte {
	return append([]byte(nil), dhPrime...)
}

// DHRandom 生成恰好 n 字节加密随机数；n 钳制到 [0, maxDHRandomLength]。
// 客户端契约要求 random 尺寸与请求一致（TDesktop 校验 random.size() 与其请求相同）。
func DHRandom(n int) ([]byte, error) {
	if n < 0 {
		n = 0
	}
	if n > maxDHRandomLength {
		n = maxDHRandomLength
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("phone: dh random: %w", err)
	}
	return buf, nil
}

func mustDecodeHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(fmt.Sprintf("phone: invalid dh prime hex: %v", err))
	}
	return b
}

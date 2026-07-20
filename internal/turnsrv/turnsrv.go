// Package turnsrv 是私聊通话 P3 的内嵌 TURN/STUN 服务（pion/turn/v5）。
//
// 角色：私聊媒体面是客户端间 P2P（tgcalls/WebRTC），服务端只在 NAT 穿透失败时
// 充当中继。本包对外只暴露三件事：服务可达地址（写进 phoneCall.connections 的
// phoneConnectionWebrtc 条目）、按通话签发的临时凭据、运行开关。
//
// 凭据采用 TURN REST 规范（draft-uberti-behave-turn-rest-00，coturn 同款）：
// username = "<unix 过期时间>:<标识>"，password = base64(HMAC-SHA1(secret, username))。
// 这使得未来切换到外部 coturn 时只需共享 secret，信令侧零改动。
package turnsrv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
	"go.uber.org/zap"
)

// Config 是内嵌 TURN 的运行参数。
type Config struct {
	// UDPPort 是 TURN/STUN 监听端口（独立于 SFU 端口：两者都要独占消费各自
	// socket 上的 STUN 流量，不能共用）。Windows 防火墙需放行。
	UDPPort int
	// AdvertiseIP 是写进 phoneConnectionWebrtc 与 relay 分配地址的客户端可达
	// 地址。⚠ 127.0.0.1 时真机拿到的 relay candidate 不可达（媒体面静默失败）。
	AdvertiseIP string
	// Realm 是 TURN long-term credential 的 realm（任意稳定串即可）。
	Realm string
	// SharedSecret 是 REST 凭据的 HMAC 密钥；为空则进程级随机生成
	// （单实例自洽；多实例/外部 coturn 必须显式配置同一值）。
	SharedSecret string
	// RelayMinPort/RelayMaxPort 限定 relay 分配的 UDP 端口段（防火墙放行范围）。
	RelayMinPort int
	RelayMaxPort int
	// CredentialTTL 是签发凭据的有效期。⚠ 必须 ≥ 单次通话时长上限：libwebrtc
	// TurnPort 的 allocation refresh 复用初始凭据，凭据过期会让进行中的中继
	// 通话断流（TDesktop 读码结论），缺省 24h。
	CredentialTTL time.Duration
	Logger        *zap.Logger
}

// Service 是信令层可见的 TURN 边界。
type Service interface {
	// Enabled 报告中继是否真实可用（false 时 connections 只能下发空列表，
	// 行为退回 P1 的 LAN 直连）。
	Enabled() bool
	// Credentials 为一通通话签发临时凭据（user 任意标识，惯例用 callID）。
	Credentials(user string) (username, password string, err error)
	// IP/Port 返回客户端可达的服务地址。
	IP() string
	Port() int
	Close() error
}

// Disabled 返回未启用实现。
func Disabled() Service { return disabled{} }

type disabled struct{}

func (disabled) Enabled() bool { return false }
func (disabled) Credentials(string) (string, string, error) {
	return "", "", fmt.Errorf("turnsrv: disabled")
}
func (disabled) IP() string   { return "" }
func (disabled) Port() int    { return 0 }
func (disabled) Close() error { return nil }

type pionTURN struct {
	cfg    Config
	server *turn.Server
}

// New 启动内嵌 TURN：绑定 UDP 端口、装配 REST 凭据校验与 relay 端口段。
func New(cfg Config) (Service, error) {
	if cfg.UDPPort <= 0 {
		return nil, fmt.Errorf("turnsrv: invalid udp port %d", cfg.UDPPort)
	}
	if cfg.AdvertiseIP == "" {
		cfg.AdvertiseIP = "127.0.0.1"
	}
	if cfg.Realm == "" {
		cfg.Realm = "telesrv"
	}
	if cfg.SharedSecret == "" {
		secret, err := randomSecret()
		if err != nil {
			return nil, err
		}
		cfg.SharedSecret = secret
	}
	if cfg.RelayMinPort <= 0 || cfg.RelayMaxPort < cfg.RelayMinPort {
		return nil, fmt.Errorf("turnsrv: invalid relay port range [%d,%d]", cfg.RelayMinPort, cfg.RelayMaxPort)
	}
	if cfg.CredentialTTL <= 0 {
		cfg.CredentialTTL = 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	relayIP := net.ParseIP(cfg.AdvertiseIP)
	if relayIP == nil {
		return nil, fmt.Errorf("turnsrv: invalid advertise ip %q", cfg.AdvertiseIP)
	}
	conn, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", cfg.UDPPort))
	if err != nil {
		return nil, fmt.Errorf("turnsrv: listen udp %d: %w", cfg.UDPPort, err)
	}
	pionLog := logging.NewDefaultLoggerFactory()
	// pion 默认只记 Error：分配/鉴权/权限请求全部静默，媒体面连不上时服务端日志
	// 一片空白、无法判断包是否到达。调到 Debug 以便区分「客户端根本没到」与
	// 「到了但被拒」。
	pionLog.DefaultLogLevel = logging.LogLevelDebug
	server, err := turn.NewServer(turn.ServerConfig{
		Realm:         cfg.Realm,
		LoggerFactory: pionLog,
		AuthHandler:   turn.LongTermTURNRESTAuthHandler(cfg.SharedSecret, pionLog.NewLogger("turn-auth")),
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: conn,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: relayIP,
				MinPort:      uint16(cfg.RelayMinPort),
				MaxPort:      uint16(cfg.RelayMaxPort),
				Address:      "0.0.0.0",
			},
		}},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("turnsrv: new server: %w", err)
	}
	cfg.Logger.Info("turn listening",
		zap.Int("udp_port", cfg.UDPPort),
		zap.String("advertise_ip", cfg.AdvertiseIP),
		zap.Int("relay_min_port", cfg.RelayMinPort),
		zap.Int("relay_max_port", cfg.RelayMaxPort))
	if cfg.AdvertiseIP == "127.0.0.1" {
		cfg.Logger.Warn("TELESRV_TURN_ADVERTISE_IP 为 127.0.0.1：真机拿到的 relay candidate 不可达（跨网媒体面静默失败），跨设备通话必须设为客户端可达 IP")
	}
	return &pionTURN{cfg: cfg, server: server}, nil
}

func (t *pionTURN) Enabled() bool { return true }

func (t *pionTURN) Credentials(user string) (string, string, error) {
	username, password, err := turn.GenerateLongTermTURNRESTCredentials(t.cfg.SharedSecret, user, t.cfg.CredentialTTL)
	if err != nil {
		return "", "", fmt.Errorf("turnsrv: generate credentials: %w", err)
	}
	return username, password, nil
}

func (t *pionTURN) IP() string   { return t.cfg.AdvertiseIP }
func (t *pionTURN) Port() int    { return t.cfg.UDPPort }
func (t *pionTURN) Close() error { return t.server.Close() }

func randomSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("turnsrv: random secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

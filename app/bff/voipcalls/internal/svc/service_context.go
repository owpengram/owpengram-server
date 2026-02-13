package svc

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/teamgram/proto/mtproto"
	"github.com/teamgram/teamgram-server/app/bff/voipcalls/internal/config"
	"github.com/teamgram/teamgram-server/app/bff/voipcalls/internal/dao"
)

type CallState int

const (
	CallStateRequested CallState = iota + 1
	CallStateWaitingIncoming
	CallStateAccepted
	CallStateEstablished
	CallStateDiscarded
)

type PrivateCallSession struct {
	ID             int64
	AccessHash     int64
	AdminID        int64
	ParticipantID  int64
	AcceptedByAuth int64
	Video          bool
	Date           int32
	StartDate      int32
	State          CallState
	Protocol       *mtproto.PhoneCallProtocol
	GAHash         []byte
	GA             []byte
	GB             []byte
	KeyFingerprint int64
	LastReason     *mtproto.PhoneCallDiscardReason
	LastDuration   int32
}

type ServiceContext struct {
	Config config.Config
	*dao.Dao

	Mu             sync.RWMutex
	CallsByID      map[int64]*PrivateCallSession
	CallsByUserKey map[string]int64
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{
		Config:         c,
		Dao:            dao.New(c),
		CallsByID:      make(map[int64]*PrivateCallSession),
		CallsByUserKey: make(map[string]int64),
	}
}

func MakePeerTag() []byte {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return buf
}

func DecodePeerTag(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 {
		return MakePeerTag()
	}
	return decoded
}

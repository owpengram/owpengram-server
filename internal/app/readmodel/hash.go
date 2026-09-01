package readmodel

import (
	"encoding/binary"
	"hash/fnv"
)

const (
	ModelDialogLight         = "dialog_light"
	ModelDialogOwner         = "dialog_owner"
	ModelContactAccount      = "contact_account"
	ModelChannelBase         = "channel_base"
	ModelChannelMember       = "channel_member"
	ModelChannelActiveIDs    = "channel_active_memberships"
	ModelChannelMediaCounts  = "channel_media_counts"
	ModelPrivateMediaCounts  = "private_media_counts"
	ModelChannelParticipants = "channel_participants"
	ModelChannelSelfBoosts   = "channel_self_boosts"
	ModelUserVisibility      = "user_visibility"
	ModelStoryPeer           = "story_peer"
	ModelStoryHiddenList     = "story_hidden_list"
)

func MixHashes(values ...int64) int64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, value := range values {
		binary.LittleEndian.PutUint64(buf[:], uint64(value))
		_, _ = h.Write(buf[:])
	}
	sum := int64(h.Sum64() & 0x7fffffffffffffff)
	if sum == 0 {
		return 1
	}
	return sum
}

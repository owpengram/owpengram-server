package dialogs

import (
	"context"
	"errors"
	"sort"

	"telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/store"
)

const dialogListSnapshotMaterializeAttempts = 2

var errDialogListSnapshotGenerationChanged = errors.New("dialog list snapshot owner generation changed")

func (s *Service) loadDialogListSnapshot(
	ctx context.Context,
	key dialogListSnapshotKey,
) (*dialogListSnapshot, error) {
	if s.sharedListCache == nil {
		list, err := s.loadDialogOwnerSnapshotHeaders(ctx, key.userID)
		if err != nil {
			return nil, err
		}
		return newDialogListSnapshot(list), nil
	}
	if s.versions == nil {
		return nil, errors.New("shared dialog list snapshot requires durable read-model versions")
	}

	for attempt := 0; attempt < dialogListSnapshotMaterializeAttempts; attempt++ {
		ownerHash, err := s.dialogOwnerHash(ctx, key.userID)
		if err != nil {
			return nil, err
		}
		snap, err := s.loadDialogListSnapshotAtOwnerHash(ctx, key, ownerHash)
		if errors.Is(err, errDialogListSnapshotGenerationChanged) {
			continue
		}
		return snap, err
	}
	return nil, errDialogListSnapshotGenerationChanged
}

func (s *Service) loadDialogListSnapshotAtOwnerHash(
	ctx context.Context,
	key dialogListSnapshotKey,
	ownerHash int64,
) (*dialogListSnapshot, error) {
	if s.sharedListCache == nil {
		list, err := s.loadDialogOwnerSnapshotHeaders(ctx, key.userID)
		if err != nil {
			return nil, err
		}
		return newDialogListSnapshot(list), nil
	}
	if s.versions == nil {
		return nil, errors.New("shared dialog list snapshot requires durable read-model versions")
	}
	if ownerHash == 0 {
		return nil, errors.New("dialog_owner read-model generation missing")
	}

	sharedKey := sharedDialogListSnapshotKey(key, ownerHash)
	cached, found, err := s.sharedListCache.GetDialogListSnapshot(ctx, sharedKey)
	if err != nil {
		return nil, err
	}
	if found {
		snap := dialogListSnapshotFromShared(cached)
		snap.ownerHash = ownerHash
		dependencyHash, err := s.dialogListSnapshotDependencyHash(ctx, ownerHash, snap)
		if err != nil {
			return nil, err
		}
		if dependencyHash == cached.DependencyHash {
			return snap, nil
		}
	}

	list, err := s.loadDialogOwnerSnapshotHeaders(ctx, key.userID)
	if err != nil {
		return nil, err
	}
	snap := newDialogListSnapshot(list)
	currentOwnerHash, err := s.dialogOwnerHash(ctx, key.userID)
	if err != nil {
		return nil, err
	}
	if currentOwnerHash != ownerHash {
		return nil, errDialogListSnapshotGenerationChanged
	}
	dependencyHash, err := s.dialogListSnapshotDependencyHash(ctx, ownerHash, snap)
	if err != nil {
		return nil, err
	}
	snap.ownerHash = ownerHash
	snap.dependencyHash = dependencyHash
	value := sharedDialogListSnapshotValue(snap, dependencyHash)
	if err := s.sharedListCache.PutDialogListSnapshot(ctx, sharedKey, value); err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Service) dialogOwnerHash(ctx context.Context, userID int64) (int64, error) {
	hash, found, err := s.versions.ReadModelHash(
		ctx, readmodel.ModelDialogOwner, userID, domain.PeerTypeUser, userID,
	)
	if err != nil {
		return 0, err
	}
	if !found || hash == 0 {
		return 0, errors.New("dialog_owner read-model generation missing")
	}
	return hash, nil
}

func (s *Service) dialogListSnapshotDependencyHash(
	ctx context.Context,
	ownerHash int64,
	snap *dialogListSnapshot,
) (int64, error) {
	peers := dialogListSnapshotPeers(snap)
	keys := make([]store.ReadModelKey, 0, len(peers))
	for _, peer := range peers {
		if peer.Type == domain.PeerTypeChannel {
			keys = append(keys, store.ReadModelKey{
				Model: readmodel.ModelChannelBase, PeerType: peer.Type, PeerID: peer.ID,
			})
		}
	}
	hashes, err := s.versions.ReadModelHashes(ctx, keys)
	if err != nil {
		return 0, err
	}
	values := make([]int64, 0, len(keys)+1)
	values = append(values, ownerHash)
	for _, key := range keys {
		hash := hashes[key]
		if hash == 0 {
			return 0, errors.New("dialog snapshot dependency generation missing")
		}
		values = append(values, hash)
	}
	return readmodel.MixHashes(values...), nil
}

func dialogListSnapshotPeers(snap *dialogListSnapshot) []domain.Peer {
	if snap == nil {
		return nil
	}
	seen := make(map[domain.Peer]struct{}, len(snap.dialogs)+1)
	peers := make([]domain.Peer, 0, len(snap.dialogs)+1)
	appendPeer := func(peer domain.Peer) {
		if peer.Type == "" || peer.ID == 0 {
			return
		}
		if _, found := seen[peer]; found {
			return
		}
		seen[peer] = struct{}{}
		peers = append(peers, peer)
	}
	for _, dialog := range snap.dialogs {
		appendPeer(dialog.Peer)
	}
	if snap.archive != nil {
		appendPeer(snap.archive.TopPeer)
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Type != peers[j].Type {
			return peers[i].Type < peers[j].Type
		}
		return peers[i].ID < peers[j].ID
	})
	return peers
}

func sharedDialogListSnapshotKey(key dialogListSnapshotKey, ownerHash int64) store.DialogListSnapshotCacheKey {
	return store.DialogListSnapshotCacheKey{
		UserID: key.userID, OwnerHash: ownerHash,
	}
}

func sharedDialogListSnapshotValue(snap *dialogListSnapshot, dependencyHash int64) store.DialogListSnapshotCacheValue {
	value := store.DialogListSnapshotCacheValue{DependencyHash: dependencyHash}
	if snap == nil {
		return value
	}
	value.Dialogs = cloneDialogSlice(snap.dialogs)
	value.Messages = cloneDialogMessages(snap.messages)
	value.Users = cloneDialogUsers(snap.users)
	value.State = snap.state
	value.ArchiveSummary = cloneDialogArchiveSummary(snap.archive)
	return value
}

func dialogListSnapshotFromShared(value store.DialogListSnapshotCacheValue) *dialogListSnapshot {
	list := domain.DialogList{
		Dialogs: value.Dialogs, Messages: value.Messages, Users: value.Users,
		State: value.State, ArchiveSummary: value.ArchiveSummary,
	}
	snap := newDialogListSnapshot(list)
	snap.dependencyHash = value.DependencyHash
	return snap
}

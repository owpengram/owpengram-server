package store

import (
	"context"

	"telesrv/internal/domain"
)

// StoryStore persists story snapshots and per-viewer story state.
type StoryStore interface {
	CreateStory(ctx context.Context, req domain.StoryCreateRequest) (domain.StoryCreateResult, error)
	UpsertStory(ctx context.Context, req domain.UpsertStoryRequest) (domain.Story, error)
	ListActiveStories(ctx context.Context, viewerUserID int64, hidden bool, now, limit int) (domain.StoryList, error)
	ListActiveStoriesPage(ctx context.Context, viewerUserID int64, hidden bool, now int, cursor domain.StoryListCursor, limit int) (domain.StoryList, error)
	ActiveStoriesDigest(ctx context.Context, viewerUserID int64, hidden bool, now int) (domain.StoryListDigest, error)
	ListOwnerActiveStories(ctx context.Context, owner domain.Peer, now, limit int) (domain.StoryList, error)
	GetPeerStories(ctx context.Context, viewerUserID int64, peer domain.Peer, now int) (domain.PeerStories, error)
	GetStoriesByID(ctx context.Context, viewerUserID int64, peer domain.Peer, ids []int, now int) (domain.StoryList, error)
	ListPinnedStories(ctx context.Context, viewerUserID int64, peer domain.Peer, offsetID, limit, now int) (domain.StoryList, error)
	HasPinnedStories(ctx context.Context, viewerUserID int64, peer domain.Peer, now int) (bool, error)
	ListStoriesArchive(ctx context.Context, viewerUserID int64, peer domain.Peer, offsetID, limit, now int) (domain.StoryList, error)
	ListReadStates(ctx context.Context, viewerUserID int64) ([]domain.StoryReadState, error)
	GetPeerMaxIDs(ctx context.Context, viewerUserID int64, peers []domain.Peer, now int) ([]domain.RecentStory, error)
	GetPeerHiddenStates(ctx context.Context, viewerUserID int64, peers []domain.Peer) (map[domain.Peer]bool, error)
	GetPeerStoryProjections(ctx context.Context, viewerUserID int64, peers []domain.Peer, now int) ([]domain.PeerStoryProjection, error)
	ActiveStoryPeerExpirations(ctx context.Context, peers []domain.Peer, now int) (map[domain.Peer]int, error)
	ListHiddenStoryPeers(ctx context.Context, viewerUserID int64) ([]domain.Peer, error)
	MarkRead(ctx context.Context, viewerUserID int64, peer domain.Peer, maxID, date int) (domain.StoryReadResult, error)
	IncrementViews(ctx context.Context, viewerUserID int64, peer domain.Peer, ids []int, date int) (int, error)
	SetReaction(ctx context.Context, viewerUserID int64, peer domain.Peer, storyID int, reaction *domain.MessageReaction, date int) (domain.StoryReactionResult, error)
	ListStoryViews(ctx context.Context, req domain.StoryViewListRequest) (domain.StoryViewList, error)
	ListStoryReactions(ctx context.Context, req domain.StoryReactionListRequest) (domain.StoryReactionList, error)
	ListStoryPublicForwards(ctx context.Context, req domain.StoryPublicForwardListRequest) (domain.StoryPublicForwardList, error)
	ListStoryViewerIDs(ctx context.Context, owner domain.Peer, storyID, limit int) ([]int64, error)
	EditStory(ctx context.Context, req domain.StoryEditRequest) (domain.StoryEditResult, error)
	DeleteStories(ctx context.Context, peer domain.Peer, ids []int, date int) (domain.StoryMutationResult, error)
	TogglePinned(ctx context.Context, peer domain.Peer, ids []int, pinned bool, date int) (domain.StoryMutationResult, error)
	TogglePinnedToTop(ctx context.Context, peer domain.Peer, ids []int) error
	SetPeerHidden(ctx context.Context, viewerUserID int64, peer domain.Peer, hidden bool) error
}

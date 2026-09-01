package stories

import (
	"context"
	"strings"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// Service owns Telegram story read/watch state.
type Service struct {
	stories       store.StoryStore
	channelAccess ChannelStoryAccess
}

// ChannelStoryAccess validates channel story mutation rights without leaking TL
// types into the app layer.
type ChannelStoryAccess interface {
	CanPostStory(ctx context.Context, userID, channelID int64) error
	CanEditStory(ctx context.Context, userID, channelID int64) error
	CanDeleteStory(ctx context.Context, userID, channelID int64) error
	CanPinStory(ctx context.Context, userID, channelID int64) error
}

// Option adjusts optional story service dependencies.
type Option func(*Service)

// WithChannelStoryAccess enables channel story publishing/admin checks.
func WithChannelStoryAccess(access ChannelStoryAccess) Option {
	return func(s *Service) { s.channelAccess = access }
}

// NewService creates a story service.
func NewService(stories store.StoryStore, opts ...Option) *Service {
	s := &Service{stories: stories}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) CreateStory(ctx context.Context, userID int64, req domain.StoryCreateRequest) (domain.StoryCreateResult, error) {
	if err := s.authorizeStoryOwner(ctx, userID, req.Owner, storyOwnerPost); err != nil {
		return domain.StoryCreateResult{}, err
	}
	if req.RandomID == 0 {
		return domain.StoryCreateResult{}, domain.ErrStoryIDInvalid
	}
	if req.Media == nil {
		return domain.StoryCreateResult{}, domain.ErrStoryNotFound
	}
	req.Period = normalizeStoryPeriod(req.Period)
	if req.Period == 0 {
		return domain.StoryCreateResult{}, domain.ErrStoryPeriodInvalid
	}
	if s == nil || s.stories == nil {
		return domain.StoryCreateResult{}, domain.ErrStoryNotFound
	}
	return s.stories.CreateStory(ctx, req)
}

// UpsertStory stores a story snapshot. It is used by tests now and by the
// publishing flow once sendStory/editStory are promoted from stubs.
func (s *Service) UpsertStory(ctx context.Context, req domain.UpsertStoryRequest) (domain.Story, error) {
	if s == nil || s.stories == nil {
		return domain.Story{}, domain.ErrStoryNotFound
	}
	return s.stories.UpsertStory(ctx, req)
}

func (s *Service) GetAllStories(ctx context.Context, viewerUserID int64, hidden bool, now, limit int) (domain.StoryList, error) {
	return s.GetAllStoriesPage(ctx, viewerUserID, hidden, now, domain.StoryListCursor{}, limit)
}

func (s *Service) GetAllStoriesPage(ctx context.Context, viewerUserID int64, hidden bool, now int, cursor domain.StoryListCursor, limit int) (domain.StoryList, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.StoryList{}, nil
	}
	if cursor.Set {
		if err := validateStoryListCursor(cursor); err != nil {
			return domain.StoryList{}, domain.ErrStoryOffsetInvalid
		}
	}
	return s.stories.ListActiveStoriesPage(ctx, viewerUserID, hidden, now, cursor, clampStoryLimit(limit))
}

func (s *Service) GetAllStoriesDigest(ctx context.Context, viewerUserID int64, hidden bool, now int) (domain.StoryListDigest, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.StoryListDigest{}, nil
	}
	return s.stories.ActiveStoriesDigest(ctx, viewerUserID, hidden, now)
}

func (s *Service) ListOwnerActiveStories(ctx context.Context, userID int64, owner domain.Peer, now, limit int) (domain.StoryList, error) {
	if err := validateStoryOwner(userID, owner); err != nil {
		return domain.StoryList{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryList{}, nil
	}
	return s.stories.ListOwnerActiveStories(ctx, owner, now, clampStoryLimit(limit))
}

func (s *Service) GetPeerStories(ctx context.Context, viewerUserID int64, peer domain.Peer, now int) (domain.PeerStories, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.PeerStories{Peer: peer}, nil
	}
	return s.stories.GetPeerStories(ctx, viewerUserID, peer, now)
}

func (s *Service) GetStoriesByID(ctx context.Context, viewerUserID int64, peer domain.Peer, ids []int, now int) (domain.StoryList, error) {
	ids, err := normalizeStoryIDsNonEmpty(ids)
	if err != nil {
		return domain.StoryList{}, err
	}
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.StoryList{}, nil
	}
	return s.stories.GetStoriesByID(ctx, viewerUserID, peer, ids, now)
}

func (s *Service) GetStoriesArchive(ctx context.Context, viewerUserID int64, peer domain.Peer, offsetID, limit, now int) (domain.StoryList, error) {
	if err := s.authorizeStoryOwner(ctx, viewerUserID, peer, storyOwnerEdit); err != nil {
		return domain.StoryList{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryList{}, nil
	}
	return s.stories.ListStoriesArchive(ctx, viewerUserID, peer, offsetID, clampStoryArchiveLimit(limit), now)
}

func (s *Service) GetPinnedStories(ctx context.Context, viewerUserID int64, peer domain.Peer, offsetID, limit, now int) (domain.StoryList, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.StoryList{}, nil
	}
	return s.stories.ListPinnedStories(ctx, viewerUserID, peer, offsetID, clampStoryLimit(limit), now)
}

func (s *Service) HasPinnedStories(ctx context.Context, viewerUserID int64, peer domain.Peer, now int) (bool, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return false, nil
	}
	return s.stories.HasPinnedStories(ctx, viewerUserID, peer, now)
}

func (s *Service) ListReadStates(ctx context.Context, viewerUserID int64) ([]domain.StoryReadState, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return nil, nil
	}
	return s.stories.ListReadStates(ctx, viewerUserID)
}

func (s *Service) GetPeerMaxIDs(ctx context.Context, viewerUserID int64, peers []domain.Peer, now int) ([]domain.RecentStory, error) {
	if len(peers) > domain.MaxStoryIDs {
		return nil, domain.ErrStoryIDInvalid
	}
	if s == nil || s.stories == nil || viewerUserID == 0 || len(peers) == 0 {
		out := make([]domain.RecentStory, len(peers))
		for i, peer := range peers {
			out[i].Peer = peer
		}
		return out, nil
	}
	return s.stories.GetPeerMaxIDs(ctx, viewerUserID, peers, now)
}

func (s *Service) GetPeerHiddenStates(ctx context.Context, viewerUserID int64, peers []domain.Peer) (map[domain.Peer]bool, error) {
	if len(peers) > domain.MaxStoryIDs {
		return nil, domain.ErrStoryIDInvalid
	}
	if s == nil || s.stories == nil || viewerUserID == 0 || len(peers) == 0 {
		return map[domain.Peer]bool{}, nil
	}
	return s.stories.GetPeerHiddenStates(ctx, viewerUserID, peers)
}

func (s *Service) GetPeerStoryProjections(ctx context.Context, viewerUserID int64, peers []domain.Peer, now int) ([]domain.PeerStoryProjection, error) {
	if len(peers) > domain.MaxStoryIDs {
		return nil, domain.ErrStoryIDInvalid
	}
	out := make([]domain.PeerStoryProjection, len(peers))
	for i, peer := range peers {
		out[i].Peer = peer
		out[i].Recent.Peer = peer
	}
	if s == nil || s.stories == nil || viewerUserID == 0 || len(peers) == 0 {
		return out, nil
	}
	return s.stories.GetPeerStoryProjections(ctx, viewerUserID, peers, now)
}

// ActiveStoryPeerExpirations returns the viewer-independent active-story gate
// used before the privacy-sensitive peer projection. A missing peer is a
// durable negative fact until its story_peer token advances.
func (s *Service) ActiveStoryPeerExpirations(ctx context.Context, peers []domain.Peer, now int) (map[domain.Peer]int, error) {
	if len(peers) > domain.MaxStoryIDs {
		return nil, domain.ErrStoryIDInvalid
	}
	if s == nil || s.stories == nil || len(peers) == 0 {
		return map[domain.Peer]int{}, nil
	}
	return s.stories.ActiveStoryPeerExpirations(ctx, peers, now)
}

// ListHiddenStoryPeers returns one sparse viewer-owned preference snapshot.
// It is intentionally separate from active-story visibility and may be empty.
func (s *Service) ListHiddenStoryPeers(ctx context.Context, viewerUserID int64) ([]domain.Peer, error) {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return nil, nil
	}
	return s.stories.ListHiddenStoryPeers(ctx, viewerUserID)
}

func (s *Service) ReadStories(ctx context.Context, viewerUserID int64, peer domain.Peer, maxID, date int) (domain.StoryReadResult, error) {
	if maxID <= 0 || maxID > domain.MaxStoryID {
		return domain.StoryReadResult{}, domain.ErrStoryIDInvalid
	}
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.StoryReadResult{ViewerID: viewerUserID, Peer: peer, Date: date}, nil
	}
	recent, err := s.stories.GetPeerMaxIDs(ctx, viewerUserID, []domain.Peer{peer}, date)
	if err != nil {
		return domain.StoryReadResult{}, err
	}
	if len(recent) == 0 || recent[0].MaxID <= 0 {
		return domain.StoryReadResult{ViewerID: viewerUserID, Peer: peer, Date: date}, nil
	}
	if maxID > recent[0].MaxID {
		maxID = recent[0].MaxID
	}
	return s.stories.MarkRead(ctx, viewerUserID, peer, maxID, date)
}

func (s *Service) IncrementViews(ctx context.Context, viewerUserID int64, peer domain.Peer, ids []int, date int) (int, error) {
	ids, err := normalizeStoryIDsNonEmpty(ids)
	if err != nil {
		return 0, err
	}
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return 0, nil
	}
	return s.stories.IncrementViews(ctx, viewerUserID, peer, ids, date)
}

func (s *Service) SendReaction(ctx context.Context, viewerUserID int64, peer domain.Peer, storyID int, reaction *domain.MessageReaction, date int) (domain.StoryReactionResult, error) {
	if storyID <= 0 || storyID > domain.MaxStoryID {
		return domain.StoryReactionResult{}, domain.ErrStoryIDInvalid
	}
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return domain.StoryReactionResult{ViewerID: viewerUserID, Peer: peer, StoryID: storyID, Reaction: reaction, Date: date}, nil
	}
	return s.stories.SetReaction(ctx, viewerUserID, peer, storyID, reaction, date)
}

func (s *Service) GetStoryViewsList(ctx context.Context, viewerUserID int64, req domain.StoryViewListRequest) (domain.StoryViewList, error) {
	if err := s.authorizeStoryOwner(ctx, viewerUserID, req.Owner, storyOwnerEdit); err != nil {
		return domain.StoryViewList{}, err
	}
	if req.StoryID <= 0 || req.StoryID > domain.MaxStoryID {
		return domain.StoryViewList{}, domain.ErrStoryIDInvalid
	}
	if err := domain.ValidateStoryInteractionOffset(req.Offset, false); err != nil {
		return domain.StoryViewList{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryViewList{}, nil
	}
	req.ViewerUserID = viewerUserID
	req.Limit = clampStoryInteractionLimit(req.Limit)
	req.Query = normalizeStoryViewQuery(req.Query)
	return s.stories.ListStoryViews(ctx, req)
}

func (s *Service) GetStoryReactionsList(ctx context.Context, viewerUserID int64, req domain.StoryReactionListRequest) (domain.StoryReactionList, error) {
	if req.StoryID <= 0 || req.StoryID > domain.MaxStoryID {
		return domain.StoryReactionList{}, domain.ErrStoryIDInvalid
	}
	if req.Owner.Type == domain.PeerTypeChannel {
		if viewerUserID == 0 || !req.CanViewOwnerInteractions {
			return domain.StoryReactionList{}, domain.ErrStoryPeerInvalid
		}
	} else {
		return domain.StoryReactionList{}, domain.ErrStoryPeerInvalid
	}
	if err := domain.ValidateStoryReactionInteractionOffset(req.Offset, req.ForwardsFirst); err != nil {
		return domain.StoryReactionList{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryReactionList{}, nil
	}
	req.ViewerUserID = viewerUserID
	req.Limit = clampStoryInteractionLimit(req.Limit)
	return s.stories.ListStoryReactions(ctx, req)
}

func (s *Service) GetStoryPublicForwards(ctx context.Context, viewerUserID int64, req domain.StoryPublicForwardListRequest) (domain.StoryPublicForwardList, error) {
	if err := s.authorizeStoryOwner(ctx, viewerUserID, req.Owner, storyOwnerEdit); err != nil {
		return domain.StoryPublicForwardList{}, err
	}
	if req.StoryID <= 0 || req.StoryID > domain.MaxStoryID {
		return domain.StoryPublicForwardList{}, domain.ErrStoryIDInvalid
	}
	if err := domain.ValidateStoryInteractionOffset(req.Offset, false); err != nil {
		return domain.StoryPublicForwardList{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryPublicForwardList{}, nil
	}
	req.ViewerUserID = viewerUserID
	req.Limit = clampStoryInteractionLimit(req.Limit)
	return s.stories.ListStoryPublicForwards(ctx, req)
}

func (s *Service) CanViewStoryStats(ctx context.Context, userID int64, peer domain.Peer) error {
	return s.authorizeStoryOwner(ctx, userID, peer, storyOwnerEdit)
}

func (s *Service) ListStoryViewerIDs(ctx context.Context, userID int64, owner domain.Peer, storyID, limit int) ([]int64, error) {
	if err := validateStoryOwner(userID, owner); err != nil {
		return nil, err
	}
	if storyID <= 0 || storyID > domain.MaxStoryID {
		return nil, domain.ErrStoryIDInvalid
	}
	if s == nil || s.stories == nil {
		return nil, nil
	}
	return s.stories.ListStoryViewerIDs(ctx, owner, storyID, clampStoryPrivacyFanoutLimit(limit))
}

func (s *Service) EditStory(ctx context.Context, userID int64, req domain.StoryEditRequest) (domain.StoryEditResult, error) {
	if err := s.authorizeStoryOwner(ctx, userID, req.Owner, storyOwnerEdit); err != nil {
		return domain.StoryEditResult{}, err
	}
	if req.ID <= 0 || req.ID > domain.MaxStoryID {
		return domain.StoryEditResult{}, domain.ErrStoryIDInvalid
	}
	if !req.UpdateMedia && !req.UpdateCaption && !req.UpdatePrivacy && !req.UpdateMediaAreas {
		return domain.StoryEditResult{}, domain.ErrStoryNotModified
	}
	if req.UpdateMedia && req.Media == nil {
		return domain.StoryEditResult{}, domain.ErrStoryNotFound
	}
	if s == nil || s.stories == nil {
		return domain.StoryEditResult{}, domain.ErrStoryNotFound
	}
	return s.stories.EditStory(ctx, req)
}

func (s *Service) DeleteStories(ctx context.Context, userID int64, peer domain.Peer, ids []int, date int) (domain.StoryMutationResult, error) {
	if err := s.authorizeStoryOwner(ctx, userID, peer, storyOwnerDelete); err != nil {
		return domain.StoryMutationResult{}, err
	}
	ids, err := normalizeStoryIDsNonEmpty(ids)
	if err != nil {
		return domain.StoryMutationResult{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryMutationResult{Peer: peer, IDs: append([]int(nil), ids...)}, nil
	}
	return s.stories.DeleteStories(ctx, peer, ids, date)
}

func (s *Service) TogglePinned(ctx context.Context, userID int64, peer domain.Peer, ids []int, pinned bool, date int) (domain.StoryMutationResult, error) {
	if err := s.authorizeStoryOwner(ctx, userID, peer, storyOwnerPin); err != nil {
		return domain.StoryMutationResult{}, err
	}
	ids, err := normalizeStoryIDs(ids)
	if err != nil {
		return domain.StoryMutationResult{}, err
	}
	if s == nil || s.stories == nil {
		return domain.StoryMutationResult{Peer: peer, IDs: append([]int(nil), ids...)}, nil
	}
	return s.stories.TogglePinned(ctx, peer, ids, pinned, date)
}

func (s *Service) TogglePinnedToTop(ctx context.Context, userID int64, peer domain.Peer, ids []int) error {
	if err := s.authorizeStoryOwner(ctx, userID, peer, storyOwnerPin); err != nil {
		return err
	}
	ids, err := normalizeStoryPinnedToTopIDs(ids)
	if err != nil {
		return err
	}
	if s == nil || s.stories == nil {
		return nil
	}
	return s.stories.TogglePinnedToTop(ctx, peer, ids)
}

func (s *Service) TogglePeerStoriesHidden(ctx context.Context, viewerUserID int64, peer domain.Peer, hidden bool) error {
	if s == nil || s.stories == nil || viewerUserID == 0 {
		return nil
	}
	return s.stories.SetPeerHidden(ctx, viewerUserID, peer, hidden)
}

func (s *Service) CanSendStory(ctx context.Context, viewerUserID int64, peer domain.Peer) (int, error) {
	if err := s.authorizeStoryOwner(ctx, viewerUserID, peer, storyOwnerPost); err != nil {
		return 0, err
	}
	return domain.DefaultStoryCanSendRemaining, nil
}

type storyOwnerAction int

const (
	storyOwnerPost storyOwnerAction = iota
	storyOwnerEdit
	storyOwnerDelete
	storyOwnerPin
)

func (s *Service) authorizeStoryOwner(ctx context.Context, userID int64, peer domain.Peer, action storyOwnerAction) error {
	if userID == 0 {
		return domain.ErrStoryPeerInvalid
	}
	switch peer.Type {
	case domain.PeerTypeUser:
		if peer.ID == userID {
			return nil
		}
	case domain.PeerTypeChannel:
		if peer.ID == 0 || s == nil || s.channelAccess == nil {
			return domain.ErrStoryPeerInvalid
		}
		switch action {
		case storyOwnerPost:
			return s.channelAccess.CanPostStory(ctx, userID, peer.ID)
		case storyOwnerEdit:
			return s.channelAccess.CanEditStory(ctx, userID, peer.ID)
		case storyOwnerDelete:
			return s.channelAccess.CanDeleteStory(ctx, userID, peer.ID)
		case storyOwnerPin:
			return s.channelAccess.CanPinStory(ctx, userID, peer.ID)
		}
	}
	return domain.ErrStoryPeerInvalid
}

func validateStoryIDs(ids []int) error {
	if len(ids) > domain.MaxStoryIDs {
		return domain.ErrStoryIDInvalid
	}
	for _, id := range ids {
		if id <= 0 || id > domain.MaxStoryID {
			return domain.ErrStoryIDInvalid
		}
	}
	return nil
}

func validateStoryIDsNonEmpty(ids []int) error {
	if len(ids) == 0 {
		return domain.ErrStoryIDInvalid
	}
	return validateStoryIDs(ids)
}

func normalizeStoryIDsNonEmpty(ids []int) ([]int, error) {
	if err := validateStoryIDsNonEmpty(ids); err != nil {
		return nil, err
	}
	return normalizeStoryIDsUnchecked(ids), nil
}

func normalizeStoryIDs(ids []int) ([]int, error) {
	if err := validateStoryIDs(ids); err != nil {
		return nil, err
	}
	return normalizeStoryIDsUnchecked(ids), nil
}

func normalizeStoryPinnedToTopIDs(ids []int) ([]int, error) {
	ids, err := normalizeStoryIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(ids) > domain.MaxStoryPinnedToTop {
		return nil, domain.ErrStoryIDInvalid
	}
	return ids, nil
}

func normalizeStoryIDsUnchecked(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func validateStoryOwner(userID int64, peer domain.Peer) error {
	if userID == 0 {
		return domain.ErrStoryPeerInvalid
	}
	if peer.Type == domain.PeerTypeUser && peer.ID == userID {
		return nil
	}
	return domain.ErrStoryPeerInvalid
}

func validateStoryListCursor(cursor domain.StoryListCursor) error {
	if !cursor.Set {
		return nil
	}
	if cursor.Date <= 0 || cursor.Peer.ID <= 0 {
		return domain.ErrStoryOffsetInvalid
	}
	switch cursor.Peer.Type {
	case domain.PeerTypeUser, domain.PeerTypeChannel:
		return nil
	default:
		return domain.ErrStoryOffsetInvalid
	}
}

func normalizeStoryPeriod(period int) int {
	if period == 0 {
		return domain.DefaultStoryPeriod
	}
	if validStoryPeriod(period) {
		return period
	}
	return 0
}

func validStoryPeriod(period int) bool {
	switch period {
	case 6 * 3600, 12 * 3600, domain.DefaultStoryPeriod, 2 * domain.DefaultStoryPeriod:
		return true
	default:
		return false
	}
}

func clampStoryLimit(limit int) int {
	if limit <= 0 || limit > domain.MaxStoryListLimit {
		return domain.MaxStoryListLimit
	}
	return limit
}

func clampStoryArchiveLimit(limit int) int {
	if limit == 0 {
		return 0
	}
	if limit < 0 || limit > domain.MaxStoryListLimit {
		return domain.MaxStoryListLimit
	}
	return limit
}

func clampStoryInteractionLimit(limit int) int {
	if limit <= 0 || limit > domain.MaxStoryInteractionListLimit {
		return domain.MaxStoryInteractionListLimit
	}
	return limit
}

func clampStoryPrivacyFanoutLimit(limit int) int {
	if limit <= 0 || limit > domain.MaxStoryPrivacyFanoutTargets {
		return domain.MaxStoryPrivacyFanoutTargets
	}
	return limit
}

func normalizeStoryViewQuery(query string) string {
	query = strings.TrimSpace(query)
	runes := []rune(query)
	if len(runes) > domain.MaxStoryViewQueryLength {
		return string(runes[:domain.MaxStoryViewQueryLength])
	}
	return query
}

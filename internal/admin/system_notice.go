package admin

import (
	"context"
	"fmt"
)

// SendSystemNotice delivers one message from the official system account
// (777000) to one user. The console uses it to tell a person that their
// private content was opened, and why.
func (s *Service) SendSystemNotice(ctx context.Context, userID int64, text string, randomID int64) error {
	if s == nil || s.broadcast == nil {
		return fmt.Errorf("admin broadcast dependency is not configured")
	}
	return s.broadcast.SendSystemNotice(ctx, userID, text, randomID)
}

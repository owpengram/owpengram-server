package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

// activityDays is how far back the overview's activity chart reaches.
const activityDays = 30

// DayActivity is one bar of the overview's activity chart. Days are UTC.
type DayActivity struct {
	Date     string `json:"date"`
	NewUsers int64  `json:"new_users"`
	Messages int64  `json:"messages"`
}

// DashboardActivity is the overview's history: per-day sign-ups and messages
// for the last activityDays, and how many accounts were seen recently.
type DashboardActivity struct {
	Days        []DayActivity `json:"days"`
	ActiveDay   int64         `json:"active_day"`
	ActiveWeek  int64         `json:"active_week"`
	ActiveMonth int64         `json:"active_month"`
}

// DashboardActivity counts by day. The message counts scan the message tables
// for the window (message_date has no index of its own), which is why the
// result is served from statsCaches rather than per request.
func (s *readStore) DashboardActivity(ctx context.Context) (DashboardActivity, error) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(activityDays - 1))
	byDay := make(map[string]*DayActivity, activityDays)
	out := DashboardActivity{Days: make([]DayActivity, activityDays)}
	for i := range out.Days {
		out.Days[i].Date = start.AddDate(0, 0, i).Format("2006-01-02")
		byDay[out.Days[i].Date] = &out.Days[i]
	}

	type dayCount struct {
		day string
		n   int64
	}
	collect := func(ctx context.Context, query string, args ...any) ([]dayCount, error) {
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var list []dayCount
		for rows.Next() {
			var c dayCount
			if err := rows.Scan(&c.day, &c.n); err != nil {
				return nil, err
			}
			list = append(list, c)
		}
		return list, rows.Err()
	}

	var users, private, channel []dayCount
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		users, err = collect(gctx, `
SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*)
FROM users
WHERE NOT is_bot AND id <> ALL($1::bigint[]) AND created_at >= $2
GROUP BY 1`, systemAccountIDs, start)
		if err != nil {
			err = fmt.Errorf("new users by day: %w", err)
		}
		return err
	})
	g.Go(func() (err error) {
		private, err = collect(gctx, `
SELECT to_char(to_timestamp(message_date) AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*)
FROM private_messages
WHERE message_date >= $1
GROUP BY 1`, start.Unix())
		if err != nil {
			err = fmt.Errorf("private messages by day: %w", err)
		}
		return err
	})
	g.Go(func() (err error) {
		channel, err = collect(gctx, `
SELECT to_char(to_timestamp(message_date) AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*)
FROM channel_messages
WHERE NOT deleted AND message_date >= $1
GROUP BY 1`, start.Unix())
		if err != nil {
			err = fmt.Errorf("channel messages by day: %w", err)
		}
		return err
	})
	g.Go(func() error {
		// last_seen_at is what the live server keeps fresh while a session is
		// online (see CountOnlineAccounts), so it is the best "was active"
		// signal the database has.
		if err := s.pool.QueryRow(gctx, `
SELECT
  count(*) FILTER (WHERE last_seen_at > extract(epoch FROM now() - interval '1 day')::bigint),
  count(*) FILTER (WHERE last_seen_at > extract(epoch FROM now() - interval '7 days')::bigint),
  count(*) FILTER (WHERE last_seen_at > extract(epoch FROM now() - interval '30 days')::bigint)
FROM users
WHERE NOT is_bot AND id <> ALL($1::bigint[])`, systemAccountIDs).Scan(&out.ActiveDay, &out.ActiveWeek, &out.ActiveMonth); err != nil {
			return fmt.Errorf("active accounts: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return DashboardActivity{}, err
	}

	for _, c := range users {
		if d, ok := byDay[c.day]; ok {
			d.NewUsers += c.n
		}
	}
	for _, list := range [][]dayCount{private, channel} {
		for _, c := range list {
			if d, ok := byDay[c.day]; ok {
				d.Messages += c.n
			}
		}
	}
	return out, nil
}

func (s *server) handleDashboardActivityAPI(w http.ResponseWriter, r *http.Request) {
	if s.read == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "read store is not configured")
		return
	}
	activity, err := s.stats.activity.Get(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

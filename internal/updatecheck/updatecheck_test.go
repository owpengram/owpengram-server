package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvaluate(t *testing.T) {
	const url = `"html_url":"https://github.com/owpengram/owpengram-server/releases/tag/O9"`
	cases := []struct {
		name, body, current string
		wantLatest          string
		wantAvailable       bool
	}{
		{"newer release", `{"tag_name":"O9",` + url + `,"body":"notes"}`, "O8", "O9", true},
		{"same release", `{"tag_name":"O8",` + url + `}`, "O8", "O8", false},
		{"older release", `{"tag_name":"O7",` + url + `}`, "O8", "O7", false},
		{"two digits beat one", `{"tag_name":"O10",` + url + `}`, "O9", "O10", true},
		{"prerelease ignored", `{"tag_name":"O9","prerelease":true,` + url + `}`, "O8", "", false},
		{"draft ignored", `{"tag_name":"O9","draft":true,` + url + `}`, "O8", "", false},
		{"foreign tag ignored", `{"tag_name":"v2.0",` + url + `}`, "O8", "", false},
		{"insecure link refused", `{"tag_name":"O9","html_url":"http://example.com/x"}`, "O8", "O9", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evaluate([]byte(tc.body), tc.current)
			if err != nil {
				t.Fatal(err)
			}
			if got.Latest != tc.wantLatest || got.Available != tc.wantAvailable {
				t.Fatalf("got latest=%q available=%v, want %q %v", got.Latest, got.Available, tc.wantLatest, tc.wantAvailable)
			}
			if got.Available && (got.URL == "" || got.Current != tc.current) {
				t.Fatalf("available status is incomplete: %+v", got)
			}
		})
	}
	if _, err := evaluate([]byte("not json"), "O8"); err == nil {
		t.Fatal("malformed JSON must be an error")
	}
}

func TestCheckRecordsAnswerAndSurvivesFailure(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub requires a User-Agent")
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"O9","html_url":"https://github.com/o/r/releases/tag/O9","body":"fixes"}`))
	}))
	defer srv.Close()

	c := New(true, "O8", srv.URL)
	if c.Snapshot().Available {
		t.Fatal("nothing is available before the first check")
	}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := c.Snapshot()
	if !got.Available || got.Latest != "O9" || got.Notes != "fixes" || got.CheckedAt.IsZero() {
		t.Fatalf("status after check = %+v", got)
	}

	status = http.StatusForbidden
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("a rate-limited answer must be an error")
	}
	if !c.Snapshot().Available {
		t.Fatal("a failed check must keep the last known answer")
	}
}

func TestRunDoesNothingWhenDisabledOrUnnumbered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	New(false, "O8", "").Run(ctx) // disabled
	New(true, "dev", "").Run(ctx) // a local build has nothing to compare against
}

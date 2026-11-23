package api_test

import (
	"encoding/json"
	"testing"
	"time"
)

// A backlog item finishes this week even though it arrived before the chart window.
// Items one millisecond apart on opposite sides of Bangkok midnight are separate days.
func TestDashboardWeeklyActivity(t *testing.T) {
	s := integrationServer(t)
	const endpoint = "/api/v1/dashboard/activity"
	if rec := apiRequest(s.handler, "GET", endpoint, "", ""); rec.Code != 401 {
		t.Fatalf("anonymous activity: %d", rec.Code)
	}
	var today time.Time
	if err := s.db.Pool.QueryRow(s.ctx, `SELECT date_trunc('day',now(),'Asia/Bangkok')`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	today = today.In(time.FixedZone("Bangkok", 7*3600))
	_, err := s.db.Pool.Exec(s.ctx, `INSERT INTO "LineGroup"(id,"lineGroupId","membershipAt","lastEventAt","updatedAt") VALUES ('activity','activity',now(),now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Pool.Exec(s.ctx, `INSERT INTO "Image"(id,"lineMessageId","lineGroupId","channelId","mediaType","lineEventTimestamp","uploadedAt","updatedAt","uploadStatus","googleMediaItemId")
	SELECT id,id,'activity','channel',media,$1::timestamptz+arrival,$1::timestamptz+completion,now(),
	CASE WHEN completion IS NULL THEN 'FAILED' ELSE 'SYNCED' END::"UploadState", CASE WHEN completion IS NULL THEN NULL ELSE id END
	FROM (VALUES
	('before','IMAGE',interval '-144 hours -1 ms',interval '-24 hours'),
	('first','VIDEO',interval '-144 hours',interval '0 hours'),
	('last','IMAGE',interval '-1 ms',NULL::interval),
	('today','IMAGE',interval '0 hours',interval '0 hours')
	) f(id,media,arrival,completion)`, today)
	if err != nil {
		t.Fatal(err)
	}
	rec := apiRequest(s.handler, "GET", endpoint, "", s.session)
	var days []struct {
		Date                                                           string
		ImagesReceived, VideosReceived, ImagesUploaded, VideosUploaded int
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &days) != nil || len(days) != 7 {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
	}
	want := [7][4]int{{0, 1, 0, 0}, {}, {}, {}, {}, {1, 0, 1, 0}, {1, 0, 1, 1}}
	for i, d := range days {
		if d.Date != today.AddDate(0, 0, i-6).Format(time.DateOnly) || [4]int{d.ImagesReceived, d.VideosReceived, d.ImagesUploaded, d.VideosUploaded} != want[i] {
			t.Errorf("unexpected day %d: %+v", i, d)
		}
	}
}

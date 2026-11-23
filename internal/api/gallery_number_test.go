package api_test

import (
	"encoding/json"
	"testing"
)

func TestGalleryDayNumbers(t *testing.T) {
	s := integrationServer(t)
	_, err := s.db.Pool.Exec(s.ctx, `INSERT INTO "LineGroup"(id,"lineGroupId","membershipAt","lastEventAt","updatedAt") VALUES ('family','family',now(),now(),now());
 INSERT INTO "Image"(id,"lineGroupId","channelId","lineMessageId","lineEventTimestamp","uploadStatus","updatedAt")
 SELECT lpad(n::text,3,'0'),'family','channel',n::text,
 CASE WHEN n=0 THEN timestamptz '2026-11-22 16:59:59Z' ELSE timestamptz '2026-11-22 17:00:00Z' END,
 CASE WHEN n=2 THEN 'FAILED' ELSE 'STAGED' END::"UploadState",now() FROM generate_series(0,37) n`)
	if err != nil {
		t.Fatal(err)
	}
	type card struct {
		ID        string
		DayNumber int
	}
	list := func(path string) []card {
		t.Helper()
		r := apiRequest(s.handler, "GET", "/api/v1/images"+path, "", s.session)
		var response struct{ Items []card }
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil {
			t.Fatalf("list: %d %s", r.Code, r.Body.String())
		}
		return response.Items
	}
	first := list("")
	if len(first) != 36 {
		t.Fatal(len(first))
	}
	for i, c := range first {
		if c.DayNumber != 37-i {
			t.Fatalf("card %s: got %d want %d", c.ID, c.DayNumber, 37-i)
		}
	}
	second := list("?page=2")
	if len(second) != 2 || second[0] != (card{"037", 1}) || second[1] != (card{"000", 1}) {
		t.Fatal(second)
	}
	filtered := list("?status=FAILED")
	if len(filtered) != 1 || filtered[0] != (card{"002", 36}) {
		t.Fatal(filtered)
	}
}

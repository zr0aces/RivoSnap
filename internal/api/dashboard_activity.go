package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"
)

type activityDay struct {
	Date           string `json:"date"`
	ImagesReceived int64  `json:"imagesReceived"`
	VideosReceived int64  `json:"videosReceived"`
	ImagesUploaded int64  `json:"imagesUploaded"`
	VideosUploaded int64  `json:"videosUploaded"`
}

// Keep the chart query off the shell-wide stats poll. A late upload contributes
// to the day it finished, independently of the day the LINE media arrived.
func (s *Server) handleDashboardActivity(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Pool.Query(r.Context(), `
		WITH bounds AS (
			SELECT (now() AT TIME ZONE 'Asia/Bangkok')::date AS today,
			       date_trunc('day', now(), 'Asia/Bangkok') - interval '144 hours' AS since
		), days AS (
			SELECT today - n AS day FROM bounds, generate_series(0,6) AS n
		), receipts AS (
			SELECT ("lineEventTimestamp" AT TIME ZONE 'Asia/Bangkok')::date AS day,
			       count(*) FILTER (WHERE "mediaType"='IMAGE') AS images,
			       count(*) FILTER (WHERE "mediaType"='VIDEO') AS videos
			FROM "Image", bounds WHERE "lineEventTimestamp" BETWEEN since AND now() GROUP BY 1
		), uploads AS (
			SELECT ("uploadedAt" AT TIME ZONE 'Asia/Bangkok')::date AS day,
			       count(*) FILTER (WHERE "mediaType"='IMAGE') AS images,
			       count(*) FILTER (WHERE "mediaType"='VIDEO') AS videos
			FROM "Image", bounds WHERE "uploadedAt" BETWEEN since AND now() GROUP BY 1
		)
		SELECT to_char(days.day,'YYYY-MM-DD'), coalesce(receipts.images,0), coalesce(receipts.videos,0),
		       coalesce(uploads.images,0), coalesce(uploads.videos,0)
		FROM days LEFT JOIN receipts USING(day) LEFT JOIN uploads USING(day) ORDER BY days.day`)
	if err != nil {
		writeErr(w, err)
		return
	}
	days, err := pgx.CollectRows(rows, pgx.RowToStructByPos[activityDay])
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, days)
}

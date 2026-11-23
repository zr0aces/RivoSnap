package model

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
)

type State string

const (
	StateReceived        State = "RECEIVED"
	StateDownloadPending State = "DOWNLOAD_PENDING"
	StateDownloading     State = "DOWNLOADING"
	StateStaged          State = "STAGED"
	StateUploadPending   State = "UPLOAD_PENDING"
	StateUploading       State = "UPLOADING"
	StateCreatingMedia   State = "CREATING_MEDIA"
	StateSynced          State = "SYNCED"
	StateRetryPending    State = "RETRY_PENDING"
	StateNeedsReview     State = "NEEDS_REVIEW"
	StateFailed          State = "FAILED"
	StateUnrecoverable   State = "UNRECOVERABLE"
)

var States = []State{
	StateReceived,
	StateDownloadPending,
	StateDownloading,
	StateStaged,
	StateUploadPending,
	StateUploading,
	StateCreatingMedia,
	StateSynced,
	StateRetryPending,
	StateNeedsReview,
	StateFailed,
	StateUnrecoverable,
}

// MaxAutoAttempts is the retry budget of one cycle: a transient failure whose budget
// is below it is retried, so a cycle has MaxAutoAttempts-1 automatic retries. Their
// waits Backoff(1)..Backoff(15) span about 3 to 6 hours, because Backoff caps at 1 hour.
const MaxAutoAttempts = 16

var Terminal = []State{
	StateSynced,
	StateNeedsReview,
	StateFailed,
	StateUnrecoverable,
}

var edges = map[State][]State{
	StateReceived:        {StateDownloadPending},
	StateDownloadPending: {StateDownloading},
	StateDownloading:     {StateStaged},
	StateStaged:          {StateUploadPending},
	StateUploadPending:   {StateUploading},
	StateUploading:       {StateCreatingMedia},
	StateCreatingMedia:   {StateSynced},
	StateRetryPending:    {StateDownloadPending, StateUploadPending},
	StateFailed:          {StateRetryPending, StateUnrecoverable},
	StateNeedsReview:     {StateSynced},
}

func IsValidState(s string) bool {
	return slices.Contains(States, State(s))
}

func IsTerminal(s State) bool {
	return slices.Contains(Terminal, s)
}

func Transition(a, b State, mediaID string) error {
	allowed := slices.Contains(edges[a], b) ||
		(!IsTerminal(a) && slices.Contains([]State{
			StateFailed,
			StateRetryPending,
			StateUnrecoverable,
			StateNeedsReview,
		}, b))
	if !allowed || (b == StateSynced && strings.TrimSpace(mediaID) == "") {
		return fmt.Errorf("invalid transition %s → %s", a, b)
	}
	return nil
}

type Fault struct {
	Code      string
	Message   string
	Transient bool
	Delay     int // milliseconds
}

func (f *Fault) Error() string {
	return f.Message
}

func NewFault(code, message string, transient bool, delay int) *Fault {
	return &Fault{
		Code:      code,
		Message:   message,
		Transient: transient,
		Delay:     delay,
	}
}

func Backoff(n, minMs int) int {
	exp := min(n, 12)
	base := min(3600000.0, 2000.0*math.Pow(2, float64(exp)))
	jitter := 0.5 + rand.Float64()*0.5
	val := int(math.Floor(base * jitter))
	if val < minMs {
		return minMs
	}
	return val
}

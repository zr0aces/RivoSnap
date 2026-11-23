package api

import "time"

// HoldHashSlots takes every password-hash slot until the returned function is called.
func (s *Server) HoldHashSlots() func() {
	for range maxHashJobs {
		s.hashSem <- struct{}{}
	}
	return func() {
		for range maxHashJobs {
			<-s.hashSem
		}
	}
}

// SetHashWait shortens how long a request waits for a password-hash slot.
func (s *Server) SetHashWait(d time.Duration) {
	s.hashWait = d
}

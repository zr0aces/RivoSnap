package testguard

import (
	"strings"
	"testing"
)

func TestCheckAcceptsLoopbackHosts(t *testing.T) {
	cases := []struct {
		name     string
		database string
		redis    string
	}{
		{"ipv4 loopback", "postgresql://u:p@127.0.0.1:55432/gallery", "redis://127.0.0.1:56379"},
		{"ipv6 loopback", "postgresql://u:p@[::1]:55432/gallery", "redis://[::1]:56379"},
		{"localhost", "postgresql://u:p@localhost:55432/gallery", "redis://localhost:56379"},
		{"redis unset", "postgresql://u:p@127.0.0.1:55432/gallery", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := check(tc.database, tc.redis); err != nil {
				t.Fatalf("check returned error for loopback hosts: %v", err)
			}
		})
	}
}

func TestCheckRefusesNonLoopbackHosts(t *testing.T) {
	cases := []struct {
		name     string
		database string
		redis    string
	}{
		{"compose host", "postgresql://u:p@postgres:5432/gallery", ""},
		{"compose host with loopback redis", "postgresql://u:p@postgres:5432/gallery", "redis://127.0.0.1:6379"},
		{"loopback database with compose redis", "postgresql://u:p@127.0.0.1:5432/gallery", "redis://redis:6379"},
		{"public address", "postgresql://u:p@203.0.113.10:5432/gallery", ""},
		{"host parameter override", "postgresql://u:p@127.0.0.1:5432/gallery?host=postgres", ""},
		{"empty database url", "", ""},
		{"unparsable database url", "postgresql://u:p@[::1:5432/gallery", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := check(tc.database, tc.redis); err == nil {
				t.Fatal("check accepted a non-loopback or missing database host")
			}
		})
	}
}

func TestCheckErrorDoesNotEchoPassword(t *testing.T) {
	err := check("postgresql://gallery:hunter2-secret@postgres:5432/gallery", "")
	if err == nil {
		t.Fatal("expected refusal for compose host")
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Fatalf("error message leaks the password: %q", err.Error())
	}
}

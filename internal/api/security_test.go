package api

import (
	"fmt"
	"strings"
	"testing"

	wa "github.com/go-webauthn/webauthn/webauthn"
)

// testOwner builds an owner with an optional TOTP seed and n enrolled passkeys.
func testOwner(seed bool, n int) *securityOwner {
	o := &securityOwner{names: map[string]string{}}
	if seed {
		o.sealedSeed = "sealed-seed"
	}
	for i := 0; i < n; i++ {
		id := []byte(fmt.Sprintf("credential-%d", i))
		o.keys = append(o.keys, wa.Credential{ID: id})
		o.names[keyID(id)] = fmt.Sprintf("Key %d", i)
	}
	return o
}

func TestValidChange(t *testing.T) {
	for _, seed := range []bool{false, true} {
		for _, n := range []int{0, 31, 32} {
			o := testOwner(seed, n)
			type change struct {
				action, target string
				want           bool
			}
			cases := []change{
				{"enable", "", !seed},
				{"disable", "", seed},
				{"replace", "", seed},
				{"regenerate", "", seed},
				{"add", "Laptop", n < 32},
				{"add", strings.Repeat("a", 80), n < 32},
				{"add", strings.Repeat("a", 81), false},
				{"add", "   ", false},
				{"remove", "missing-id", false},
				{"unknown", "", false},
			}
			if n > 0 {
				cases = append(cases, change{"remove", keyID([]byte("credential-0")), true})
			}
			for _, c := range cases {
				if got := validChange(o, securityInput{Action: action(c.action), Target: c.target}); got != c.want {
					t.Errorf("seed=%t passkeys=%d action=%q target=%q: got %t want %t", seed, n, c.action, c.target, got, c.want)
				}
			}
		}
	}
}

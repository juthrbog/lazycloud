//go:build smoke

package smoke

import (
	"testing"

	"github.com/juthrbog/lazycloud/internal/config"
)

// TestIdentityInTheHeader checks that the header shows the account that
// STS names and the endpoint badge, and that :whoami shows the ARN.
func TestIdentityInTheHeader(t *testing.T) {
	h := start(t, "")
	h.waitFor("000000000000", "endpoint")

	h.keys(":whoami")
	h.enter()
	h.waitFor("ARN", "arn:aws:", "Endpoint", "Mode         ReadWrite")
}

// TestAccountRuleLocksTheSession starts with --read-write and a rule that
// locks the LocalStack account. The request waits for STS, then the rule
// refuses it, and the mode picker refuses ReadWrite too.
func TestAccountRuleLocksTheSession(t *testing.T) {
	h := startWith(t, "", func(c *config.Config) {
		c.Rules = []config.Rule{{Name: "local", Accounts: []string{"000000000000"}, ReadOnly: true, Label: "LOCAL"}}
	}, "RO")
	h.waitFor("--read-write ignored: profile default is locked to ReadOnly by rule local", "LOCAL")
	// The refusal of the picker has the same words, so the first toast
	// must go first.
	h.waitGone("--read-write ignored")

	h.keys(":mode")
	h.enter()
	// The suggestion of the command bar also says ReadWrite.
	h.waitFor("Access Mode")
	h.pick("ReadWrite   (locked by rule local)")
	h.waitFor("default is locked to ReadOnly by rule local")
}

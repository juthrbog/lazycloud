package config

import (
	"fmt"
	"path"
	"slices"
	"unicode/utf8"
)

// Rule locks sessions to ReadOnly, or gives them a label in the header. A
// rule matches a session by the profile name or by the account ID. Flags
// and env vars do not change rules.
type Rule struct {
	Name     string   `toml:"name"`      // shown in messages: locked by rule "prod"
	Profiles []string `toml:"profiles"`  // globs with path.Match syntax
	Accounts []string `toml:"accounts"`  // 12-digit account IDs
	ReadOnly bool     `toml:"read_only"` // lock a matching session to ReadOnly
	Label    string   `toml:"label"`     // header badge, at most MaxLabelLen characters
}

// MaxLabelLen is the longest label that a rule can have.
const MaxLabelLen = 8

// Match is the result of the rules for one session.
type Match struct {
	Lock       string // the name of the first rule that locks the session, "" when none locks it
	Label      string // the label of the first matching rule that has one
	LabelLocks bool   // the rule of the label locks the session
}

// MatchSession applies the rules to a session. The session is locked when
// any matching rule has read_only. An empty profile is the profile
// "default". An empty account matches no account rule.
func (c Config) MatchSession(profile, account string) Match {
	if profile == "" {
		profile = "default"
	}
	var m Match
	for _, r := range c.Rules {
		if !r.matches(profile, account) {
			continue
		}
		if r.ReadOnly && m.Lock == "" {
			m.Lock = r.Name
		}
		if r.Label != "" && m.Label == "" {
			m.Label, m.LabelLocks = r.Label, r.ReadOnly
		}
	}
	return m
}

// HasAccountRules reports whether any rule matches by account.
func (c Config) HasAccountRules() bool {
	for _, r := range c.Rules {
		if len(r.Accounts) > 0 {
			return true
		}
	}
	return false
}

func (r Rule) matches(profile, account string) bool {
	for _, p := range r.Profiles {
		if ok, _ := path.Match(p, profile); ok {
			return true
		}
	}
	if account == "" {
		return false
	}
	return slices.Contains(r.Accounts, account)
}

// checkRules drops the bad entries of each rule and returns a warning for
// each. A bad entry does not drop the rule, so a typo in one glob does not
// unlock the other matches of the rule. A rule without a name gets the
// name "#N", its place in the file.
func (c *Config) checkRules(file string) []string {
	var warnings []string
	for i := range c.Rules {
		r := &c.Rules[i]
		if r.Name == "" {
			r.Name = fmt.Sprintf("#%d", i+1)
		}
		warn := func(format string, a ...any) {
			warnings = append(warnings, fmt.Sprintf("rule %q in %s: ", r.Name, file)+fmt.Sprintf(format, a...))
		}
		profiles := r.Profiles[:0:0]
		for _, p := range r.Profiles {
			if _, err := path.Match(p, ""); err != nil {
				warn("bad profile pattern %q", p)
				continue
			}
			profiles = append(profiles, p)
		}
		r.Profiles = profiles
		accounts := r.Accounts[:0:0]
		for _, a := range r.Accounts {
			if !isAccountID(a) {
				warn("account %q is not a 12-digit account ID", a)
				continue
			}
			accounts = append(accounts, a)
		}
		r.Accounts = accounts
		if utf8.RuneCountInString(r.Label) > MaxLabelLen {
			warn("label %q is longer than %d characters, so it is not shown", r.Label, MaxLabelLen)
			r.Label = ""
		}
		if len(r.Profiles) == 0 && len(r.Accounts) == 0 {
			warn("the rule has no profiles and no accounts, so it matches nothing")
		}
	}
	return warnings
}

func isAccountID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

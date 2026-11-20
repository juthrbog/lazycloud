package registry

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/ui"
)

func TestEveryServiceFeatureHasNavCommand(t *testing.T) {
	for _, svc := range Services {
		for _, feat := range svc.Features {
			found := false
			for _, cmd := range Commands {
				if cmd.ViewID == feat.ViewID {
					found = true
					break
				}
			}
			assert.True(t, found, "service %s feature %s (ViewID=%s) has no matching nav command", svc.Name, feat.Name, feat.ViewID)
		}
	}
}

// Each name and alias belongs to one command. LookupCommand takes the
// first match, so a second command with the same word could never run.
func TestCommandWordsAreUnique(t *testing.T) {
	owner := map[string]string{}
	for _, cmd := range Commands {
		for _, w := range append([]string{cmd.Name}, cmd.Aliases...) {
			if prev, ok := owner[w]; ok {
				t.Errorf("%q belongs to %q and %q", w, prev, cmd.Name)
			}
			owner[w] = cmd.Name
		}
	}
}

// The words "logs", "log" and "events" stay free for AWS services, for
// example CloudWatch Logs.
func TestServiceWordsAreFree(t *testing.T) {
	for _, w := range []string{"logs", "log", "events"} {
		assert.Nil(t, LookupCommand(w), "%q", w)
	}
}

// Each icon of the home screen has a fallback of one plain character, as
// TestIconFallbacksArePlainCharacters in ui checks for the icons it names.
// This test covers the icon of a new service too.
func TestServiceIconsHavePlainFallbacks(t *testing.T) {
	check := func(name string, icon ui.ServiceIcon) {
		assert.NotEmpty(t, icon.Nerd, "%s has no Nerd Font icon", name)
		assert.Equal(t, 1, ansi.StringWidth(icon.Fallback), "%s fallback %q", name, icon.Fallback)
		for _, r := range icon.Fallback {
			assert.Less(t, r, rune(0x1F000), "%s fallback %q is an emoji", name, icon.Fallback)
			assert.NotEqual(t, rune(0xFE0F), r, "%s fallback %q asks for an emoji", name, icon.Fallback)
		}
	}
	for _, s := range Services {
		check(s.Name, s.Icon)
		for _, f := range s.Features {
			check(s.Name+" "+f.Name, f.Icon)
		}
	}
}

func TestCommandBarEntriesCount(t *testing.T) {
	entries := CommandBarEntries()
	assert.Len(t, entries, len(Commands))
}

func TestCommandBarEntriesValues(t *testing.T) {
	entries := CommandBarEntries()
	for i, e := range entries {
		assert.Equal(t, Commands[i].Name, e.Name)
		assert.Equal(t, Commands[i].Description, e.Description)
	}
}

func TestLookupCommandByName(t *testing.T) {
	cmd := LookupCommand("ec2")
	assert.NotNil(t, cmd)
	assert.Equal(t, "ec2_list", cmd.ViewID)
}

func TestLookupSubResourceCommand(t *testing.T) {
	cmd := LookupCommand("ec2/amis")
	assert.NotNil(t, cmd)
	assert.Equal(t, "ami_list", cmd.ViewID)
}

func TestLookupCommandByAlias(t *testing.T) {
	cmd := LookupCommand("q")
	assert.NotNil(t, cmd)
	assert.Equal(t, "quit", cmd.Name)

	cmd = LookupCommand("eventlog")
	assert.NotNil(t, cmd)
	assert.Equal(t, "eventlog", cmd.ViewID)

	cmd = LookupCommand("instances")
	assert.NotNil(t, cmd)
	assert.Equal(t, "ec2", cmd.Name)

	cmd = LookupCommand("amis")
	assert.NotNil(t, cmd)
	assert.Equal(t, "ec2/amis", cmd.Name)
}

func TestLookupCommandNotFound(t *testing.T) {
	cmd := LookupCommand("nonexistent")
	assert.Nil(t, cmd)
}

func TestNavCommands(t *testing.T) {
	navCount := 0
	for _, cmd := range Commands {
		if cmd.IsNav() {
			navCount++
			assert.NotEmpty(t, cmd.ViewID)
		}
	}
	assert.Positive(t, navCount, "should have at least one nav command")
}

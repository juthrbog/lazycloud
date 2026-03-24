package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestPickerOptionsCount(t *testing.T) {
	opts := PickerOptions()
	assert.Equal(t, len(Commands), len(opts))
}

func TestPickerOptionsValues(t *testing.T) {
	opts := PickerOptions()
	for i, opt := range opts {
		assert.Equal(t, Commands[i].Name, opt.Value)
		assert.Contains(t, opt.Label, Commands[i].Description)
	}
}

func TestLookupCommandByName(t *testing.T) {
	cmd := LookupCommand("ec2")
	assert.NotNil(t, cmd)
	assert.Equal(t, "ec2_list", cmd.ViewID)
}

func TestLookupCommandByAlias(t *testing.T) {
	cmd := LookupCommand("q")
	assert.NotNil(t, cmd)
	assert.Equal(t, "quit", cmd.Name)

	cmd = LookupCommand("events")
	assert.NotNil(t, cmd)
	assert.Equal(t, "logs", cmd.Name)

	cmd = LookupCommand("instances")
	assert.NotNil(t, cmd)
	assert.Equal(t, "ec2", cmd.Name)
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
	assert.Greater(t, navCount, 0, "should have at least one nav command")
}

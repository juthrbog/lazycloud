// Package registry provides a single source of truth for service and command
// definitions used by the home view, command palette, and view factory.
package registry

import (
	"fmt"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// Feature is a navigable sub-resource within a service (e.g., "Instances" under EC2).
type Feature struct {
	Name   string
	ViewID string
	Icon   ui.ServiceIcon
}

// Service is a top-level AWS service shown in the home grid.
type Service struct {
	Name     string
	Icon     ui.ServiceIcon
	Features []Feature
}

// Command is a palette entry — either a navigation target or an app action.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	ViewID      string // empty for non-navigation commands (quit, mode, theme, etc.)
}

// IsNav reports whether this command navigates to a view.
func (c Command) IsNav() bool { return c.ViewID != "" }

// Services is the ordered list of AWS services displayed on the home screen.
var Services = []Service{
	{Name: "EC2", Icon: ui.IconEC2, Features: []Feature{
		{Name: "Instances", ViewID: "ec2_list", Icon: ui.IconEC2},
		{Name: "AMIs", ViewID: "ami_list", Icon: ui.IconCloud},
	}},
	{Name: "S3", Icon: ui.IconS3, Features: []Feature{
		{Name: "Buckets", ViewID: "s3_list", Icon: ui.IconS3},
	}},
}

// Commands is the ordered list of commands available in the command palette.
var Commands = []Command{
	{Name: "quit", Aliases: []string{"q", "qa", "qall"}, Description: "Exit LazyCloud"},
	{Name: "home", Description: "Go to home screen"},
	{Name: "ec2", Aliases: []string{"instances"}, Description: "EC2 instances", ViewID: "ec2_list"},
	{Name: "amis", Description: "EC2 AMIs", ViewID: "ami_list"},
	{Name: "s3", Aliases: []string{"buckets"}, Description: "S3 buckets", ViewID: "s3_list"},
	{Name: "logs", Aliases: []string{"log", "events"}, Description: "Event log", ViewID: "eventlog"},
	{Name: "mode", Description: "Toggle ReadOnly/ReadWrite"},
	{Name: "theme", Description: "Switch theme"},
	{Name: "region", Description: "Switch region"},
	{Name: "profile", Description: "Switch profile"},
}

// PickerOptions builds the command palette options for the picker.
func PickerOptions() []ui.PickerOption {
	opts := make([]ui.PickerOption, len(Commands))
	for i, c := range Commands {
		opts[i] = ui.PickerOption{
			Label: fmt.Sprintf("%-14s %s", c.Name, c.Description),
			Value: c.Name,
		}
	}
	return opts
}

// LookupCommand finds a command by name or alias. Returns nil if not found.
func LookupCommand(input string) *Command {
	for i := range Commands {
		if Commands[i].Name == input {
			return &Commands[i]
		}
		for _, alias := range Commands[i].Aliases {
			if alias == input {
				return &Commands[i]
			}
		}
	}
	return nil
}

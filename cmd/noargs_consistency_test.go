package cmd

import "testing"

// Commands that take no positional arguments must reject extras instead of
// silently ignoring them; silently dropping user input masks mistakes.
func TestNoArgsCommandsRejectPositionalArgs(t *testing.T) {
	for _, command := range []struct {
		name string
		args func() error
	}{
		{"list", func() error { return listCmd.Args(listCmd, []string{"extra"}) }},
		{"status", func() error { return statusCmd.Args(statusCmd, []string{"extra"}) }},
		{"region list", func() error { return regionListCmd.Args(regionListCmd, []string{"extra"}) }},
		{"region current", func() error { return regionCurrentCmd.Args(regionCurrentCmd, []string{"extra"}) }},
		{"profile list", func() error { return profileListCmd.Args(profileListCmd, []string{"extra"}) }},
		{"profile current", func() error { return profileCurrentCmd.Args(profileCurrentCmd, []string{"extra"}) }},
		{"apply", func() error { return applyCmd.Args(applyCmd, []string{"extra"}) }},
		{"logout", func() error { return logoutCmd.Args(logoutCmd, []string{"extra"}) }},
	} {
		t.Run(command.name, func(t *testing.T) {
			if err := command.args(); err == nil {
				t.Fatalf("%s should reject positional arguments", command.name)
			}
		})
	}
}

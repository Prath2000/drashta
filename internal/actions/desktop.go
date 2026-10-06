package actions

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Prath2000/drashta/internal/pathguard"
)

// DesktopModes are the Plash desktop modes the plash-mode script knows
// (contrib/desktop/README.md): browse, wallpaper and blank (the normal wallpaper).
var DesktopModes = []string{"browse", "wallpaper", "blank"}

// PlanDesktop builds "<DesktopScript> <mode>". mode must be one of DesktopModes
// (ErrInvalid). The script comes only from config.toml; it must exist and be
// inside c.Roots (ErrForbidden), and an empty DesktopScript is ErrAction.
func (c Config) PlanDesktop(mode string) (Command, error) {
	if !slices.Contains(DesktopModes, mode) {
		return Command{}, fmt.Errorf("%w: unknown desktop mode %q", ErrInvalid, mode)
	}
	if c.DesktopScript == "" {
		return Command{}, fmt.Errorf("%w: no desktop_script in config.toml", ErrAction)
	}
	p, err := pathguard.ResolveAny(c.Roots, c.DesktopScript)
	if err != nil {
		return Command{}, guardErr(err)
	}
	return Command{Name: p, Args: []string{mode}}, nil
}

// ReadDesktopMode returns the mode plash-mode last set, from its state file
// (one word). It is "" when the file is missing or holds anything else.
func ReadDesktopMode(stateFile string) string {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return ""
	}
	m := strings.TrimSpace(string(b))
	if !slices.Contains(DesktopModes, m) {
		return ""
	}
	return m
}

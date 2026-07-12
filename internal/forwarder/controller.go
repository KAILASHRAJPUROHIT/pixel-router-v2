package forwarder

import (
	"fmt"
	"os/exec"
	"regexp"
)

type RealController struct {
	Package string
}

var enabledStateRe = regexp.MustCompile(`enabled=(\d)`)

func (r RealController) IsEnabled() (bool, error) {
	out, err := exec.Command("pm", "dump", r.Package).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("pm dump: %w", err)
	}
	m := enabledStateRe.FindSubmatch(out)
	if m == nil {
		return false, fmt.Errorf("could not find enabled state for %s", r.Package)
	}
	return string(m[1]) == "1", nil
}

func (r RealController) Enable() error {
	return exec.Command("pm", "enable", r.Package).Run()
}

func (r RealController) IsPermissionGranted(permission string) (bool, error) {
	out, err := exec.Command("dumpsys", "package", r.Package).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys package: %w", err)
	}
	re := regexp.MustCompile(`android\.permission\.` + regexp.QuoteMeta(permission) + `:[\s\S]{0,80}?granted=true`)
	return re.Match(out), nil
}

func (r RealController) GrantPermission(permission string) error {
	return exec.Command("pm", "grant", r.Package, "android.permission."+permission).Run()
}

func (r RealController) IsOnDozeWhitelist() (bool, error) {
	out, err := exec.Command("dumpsys", "deviceidle", "whitelist").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("dumpsys deviceidle: %w", err)
	}
	re := regexp.MustCompile(regexp.QuoteMeta(r.Package))
	return re.Match(out), nil
}

func (r RealController) AddToDozeWhitelist() error {
	return exec.Command("dumpsys", "deviceidle", "whitelist", "+"+r.Package).Run()
}

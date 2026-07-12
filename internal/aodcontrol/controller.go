package aodcontrol

import (
	"os"
	"os/exec"
	"time"
)

const PrefsPath = "/data/data/com.router.aod/shared_prefs/aod_prefs.xml"

type Controller interface {
	ReadPrefs() (string, error)
	WritePrefs(xml string) error
	// Refresh force-stops the app and toggles the screensaver off/on
	// then wakes the device - a plain wake alone only resumes the
	// cached dream, it doesn't reload the layout/theme resource, per
	// V1's own confirmed finding.
	Refresh() error
}

type RealController struct{}

func (RealController) ReadPrefs() (string, error) {
	data, err := os.ReadFile(PrefsPath)
	return string(data), err
}

func (RealController) WritePrefs(xml string) error {
	return os.WriteFile(PrefsPath, []byte(xml), 0644)
}

func (RealController) Refresh() error {
	exec.Command("am", "force-stop", "com.router.aod").Run()
	exec.Command("settings", "put", "secure", "screensaver_enabled", "0").Run()
	time.Sleep(1 * time.Second)
	if err := exec.Command("settings", "put", "secure", "screensaver_enabled", "1").Run(); err != nil {
		return err
	}
	return exec.Command("input", "keyevent", "224").Run()
}

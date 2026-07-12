package failover

import (
	"bufio"
	"os"
	"strings"
)

// Phone is one family member's iPhone, as V1's failover.properties
// already stores it (phoneN.name/number/ssid/password/mac/bt_mac).
type Phone struct {
	Name     string
	Number   string
	SSID     string
	Password string
	MAC      string // WiFi MAC, as seen on our own hotspot's client list
	BTMac    string // Bluetooth MAC, separate from the WiFi MAC
}

type Config struct {
	Phone1 Phone
	Phone2 Phone
}

// LoadConfig reads the exact same properties file V1's
// failover_monitor.sh already uses (read-only - this is real family
// contact/credential data, not something V2 should duplicate into a
// second file).
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[parts[0]] = parts[1]
	}

	cfg := Config{
		Phone1: Phone{
			Name:     valueOr(values["phone1.name"], "Phone 1"),
			Number:   values["phone1.number"],
			SSID:     values["phone1.ssid"],
			Password: values["phone1.password"],
			MAC:      values["phone1.mac"],
			BTMac:    values["phone1.bt_mac"],
		},
		Phone2: Phone{
			Name:     valueOr(values["phone2.name"], "Phone 2"),
			Number:   values["phone2.number"],
			SSID:     values["phone2.ssid"],
			Password: values["phone2.password"],
			MAC:      values["phone2.mac"],
			BTMac:    values["phone2.bt_mac"],
		},
	}
	return cfg, scanner.Err()
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

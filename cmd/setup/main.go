// setup is a one-time (or re-run-anytime) CLI that generates a fresh
// password hash, TOTP secret, and recovery codes together, and prints
// everything needed to configure the webserver and enroll in Authy -
// this is the only time recovery codes and the raw password are ever
// visible, so this must be run somewhere you can safely capture the
// output (not logged, not committed).
package main

import (
	"bufio"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/antriva/pixel-router-v2/internal/store"
)

const totpAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func generateTotpSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(totpAlphabet[int(x)%len(totpAlphabet)])
	}
	return sb.String(), nil
}

func main() {
	username := flag.String("username", "Antriva", "dashboard username")
	dbPath := flag.String("db", "", "path to SQLite state db (recovery codes get stored here); leave empty to skip storing them")
	numRecoveryCodes := flag.Int("recovery-codes", 10, "number of recovery codes to generate")
	flag.Parse()

	fmt.Print("Enter new dashboard password: ")
	reader := bufio.NewReader(os.Stdin)
	password, _ := reader.ReadString('\n')
	password = strings.TrimSpace(password)
	if password == "" {
		fmt.Fprintln(os.Stderr, "password cannot be empty")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bcrypt error:", err)
		os.Exit(1)
	}

	secret, err := generateTotpSecret()
	if err != nil {
		fmt.Fprintln(os.Stderr, "secret generation error:", err)
		os.Exit(1)
	}

	var codes []string
	if *dbPath != "" {
		st, err := store.Open(*dbPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open store:", err)
			os.Exit(1)
		}
		defer st.Close()
		codes, err = st.GenerateRecoveryCodes(*numRecoveryCodes)
		if err != nil {
			fmt.Fprintln(os.Stderr, "generate recovery codes:", err)
			os.Exit(1)
		}
	}

	fmt.Println()
	fmt.Println("=== Save this output somewhere safe - the password and recovery codes are never shown again ===")
	fmt.Println()
	fmt.Println("Username:", *username)
	fmt.Println("Password hash (pass to webserver --password-hash):")
	fmt.Println("  " + string(hash))
	fmt.Println()
	fmt.Println("TOTP secret - add this to Authy manually (not a QR scan):")
	fmt.Println("  " + secret)
	fmt.Println()
	if len(codes) > 0 {
		fmt.Println("Recovery codes (one-time use each, for when Authy is unavailable):")
		for _, c := range codes {
			fmt.Println("  " + c)
		}
		fmt.Println()
	}
	fmt.Println("otpauth URI (some apps accept pasting this directly):")
	fmt.Printf("  otpauth://totp/PixelRouterV2:%s?secret=%s&issuer=PixelRouterV2&algorithm=SHA1&digits=6&period=30\n", *username, secret)
}

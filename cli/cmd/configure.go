package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// configureCmd writes/updates the shared config file under
// $XDG_CONFIG_HOME/ncloud-community/configure. Modeled on `aws
// configure` — interactively prompt for the four core fields, fall
// back to existing values shown in [brackets] so re-running on an
// already-populated profile is just confirmations.
var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Write IAM credentials and defaults to the shared config file.",
	Long: `Interactively populate ~/.config/ncloud-community/configure.

Prompts for IAM access key + secret, default region, and default
environment. Re-running on an existing profile shows current values in
[brackets] — press Enter to keep them.

Use --profile to target a non-DEFAULT section so multiple accounts
(e.g. personal vs gov-account) can coexist.`,
	RunE: runConfigure,
}

func init() {
	rootCmd.AddCommand(configureCmd)
}

func runConfigure(cmd *cobra.Command, _ []string) error {
	profile := flagProfile
	if profile == "" {
		profile = "DEFAULT"
	}

	path, err := configurePath()
	if err != nil {
		return err
	}

	// Load existing profile contents to use as prompt defaults.
	existing := readProfile(path, profile)

	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()

	access, err := prompt(reader, out, "NCP Access Key ID", existing["ncloud_access_key_id"], false)
	if err != nil {
		return err
	}
	secret, err := prompt(reader, out, "NCP Secret Key", existing["ncloud_secret_access_key"], true)
	if err != nil {
		return err
	}
	region, err := prompt(reader, out, "Default region", coalesce(existing["ncloud_region"], "kr"), false)
	if err != nil {
		return err
	}
	env, err := prompt(reader, out, "Default environment", coalesce(existing["ncloud_environment"], "public"), false)
	if err != nil {
		return err
	}

	switch env {
	case "public", "financial", "gov":
	default:
		return fmt.Errorf("environment must be one of public|financial|gov, got %q", env)
	}

	values := map[string]string{
		"ncloud_access_key_id":     access,
		"ncloud_secret_access_key": secret,
		"ncloud_region":            region,
		"ncloud_environment":       env,
	}
	if err := writeProfile(path, profile, values); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nWrote profile [%s] to %s\n", profile, path)
	return nil
}

// configurePath returns the canonical XDG location, creating parent
// directories if needed.
func configurePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	dir := filepath.Join(xdg, "ncloud-community")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "configure"), nil
}

// prompt renders "Label [default]: " and reads a line. If user
// presses Enter on a non-empty default, returns the default.
//
// When secret == true AND stdin is a TTY, we use term.ReadPassword
// to disable terminal echo so the typed value never appears on
// screen and never lands in scrollback. When stdin is NOT a TTY
// (e.g. tests piping `printf '...\n...' | ncc configure`) we fall
// back to the bufio.Reader path so automation still works — there's
// no echo to suppress on a pipe anyway.
func prompt(r *bufio.Reader, w io.Writer, label, def string, secret bool) (string, error) {
	hint := ""
	if def != "" {
		if secret {
			hint = " [***]"
		} else {
			hint = " [" + def + "]"
		}
	}
	fmt.Fprintf(w, "%s%s: ", label, hint)

	if secret && term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		// term.ReadPassword swallows the trailing newline the user
		// typed, so add one back to the displayed line.
		fmt.Fprintln(w)
		if err != nil {
			return "", err
		}
		line := strings.TrimSpace(string(raw))
		if line == "" {
			return def, nil
		}
		return line, nil
	}

	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readProfile loads the named profile out of an ini-style file. Missing
// file → empty map. Format mirrors the SDK's ini parser
// (auth/credentials.go's parseIni).
func readProfile(path, profile string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	in := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.Trim(line, "[]") == profile
			continue
		}
		if !in {
			continue
		}
		if eq := strings.Index(line, "="); eq > 0 {
			out[strings.TrimSpace(line[:eq])] = strings.TrimSpace(line[eq+1:])
		}
	}
	return out
}

// writeProfile rewrites the named profile section in place, leaving
// other profiles untouched. If the file doesn't exist it's created.
// If the profile is new it's appended.
func writeProfile(path, profile string, values map[string]string) error {
	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	}

	// Replace the target [profile] block, or append if not present.
	lines := strings.Split(string(existing), "\n")
	var (
		out      []string
		inTarget bool
		written  bool
	)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			if inTarget {
				// We were inside the target section; emit the new block once.
				out = append(out, renderProfile(profile, values)...)
				written = true
			}
			inTarget = strings.Trim(t, "[]") == profile
			if inTarget {
				// Skip the original target header — we'll re-emit it below.
				continue
			}
		}
		if inTarget {
			continue
		}
		out = append(out, line)
	}
	if inTarget && !written {
		// Target was the last section in the file.
		out = append(out, renderProfile(profile, values)...)
	}
	if !inTarget && !written {
		// Profile wasn't present; append.
		// Trim trailing blank lines so the appended block starts cleanly.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, renderProfile(profile, values)...)
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}

func renderProfile(profile string, v map[string]string) []string {
	keys := []string{"ncloud_access_key_id", "ncloud_secret_access_key", "ncloud_region", "ncloud_environment"}
	out := []string{"[" + profile + "]"}
	for _, k := range keys {
		if v[k] == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%-25s = %s", k, v[k]))
	}
	return out
}

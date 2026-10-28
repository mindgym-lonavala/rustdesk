// invite-helper is downloaded from an invite link as
// ManashaktiSupport-Help-<token>.exe or ManashaktiSupport-Setup-<token>.exe.
//
// Help:  downloads the portable client, starts it and reports its ID, so the technician
//
//	sees "Invite: <name>" in the address book. The user accepts the session.
//
// Setup: (elevated) installs the MSI, sets a random permanent password and reports
//
//	ID + password, so the PC appears in the address book for unattended access.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

// Set at build time: -ldflags "-X main.baseURL=https://support.manashakti.org:8443"
var baseURL = "https://support.manashakti.org:8443"

const appName = "ManashaktiSupport"

var (
	reFileName = regexp.MustCompile(`(?i)ManashaktiSupport-(Help|Setup)-([A-Za-z0-9_-]{43})`)
	reID       = regexp.MustCompile(`^\d{6,12}$`)
)

// parseInvite extracts mode and token from the helper's own file name.
// Browsers may append " (1)" to repeated downloads; that is tolerated.
func parseInvite(exePath string) (mode, token string, err error) {
	m := reFileName.FindStringSubmatch(filepath.Base(exePath))
	if m == nil {
		return "", "", errors.New("this file was renamed; download it again from your invite link")
	}
	return strings.ToLower(m[1]), m[2], nil
}

// lastID returns the last line of output that looks like a device ID.
func lastID(out []byte) string {
	id := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); reID.MatchString(l) {
			id = l
		}
	}
	return id
}

const pwAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(pwAlphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = pwAlphabet[v.Int64()]
	}
	return string(b), nil
}

type inviteInfo struct {
	Name       string            `json:"name"`
	Technician string            `json:"technician"`
	AllowSetup bool              `json:"allow_setup"`
	SHA256     map[string]string `json:"sha256"`
}

var httpClient = &http.Client{Timeout: 15 * time.Minute}

func apiError(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	if s := strings.TrimSpace(string(b)); s != "" && len(s) < 200 {
		return errors.New(s)
	}
	return fmt.Errorf("server returned %s", resp.Status)
}

func getInfo(token string) (*inviteInfo, error) {
	resp, err := httpClient.Get(baseURL + "/api/invite/" + token)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Manashakti Support server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var info inviteInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// download fetches a file and verifies its SHA-256 when the server provided one.
func download(token, kind, dest, wantSHA string) error {
	resp, err := httpClient.Get(baseURL + "/api/invite/" + token + "/file/" + kind)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); wantSHA != "" && !strings.EqualFold(got, wantSHA) {
		os.Remove(tmp)
		return errors.New("downloaded file is damaged (checksum mismatch); try again")
	}
	os.Remove(dest)
	return os.Rename(tmp, dest)
}

func enroll(token string, body map[string]string) error {
	b, _ := json.Marshal(body)
	resp, err := httpClient.Post(baseURL+"/api/invite/"+token+"/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	return nil
}

// waitForID polls `exe --get-id` until it prints an ID.
func waitForID(exe string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := hiddenCommand(exe, "--get-id").Output()
		if id := lastID(out); id != "" {
			return id, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("Manashakti Support did not report its ID in time")
		}
		time.Sleep(3 * time.Second)
	}
}

func hostInfo() (string, string) {
	h, _ := os.Hostname()
	return h, os.Getenv("USERNAME")
}

func runHelp(token string, info *inviteInfo) error {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), appName+"-Invite")
	exe := filepath.Join(dir, appName+".exe")
	step("Downloading Manashakti Support")
	if err := download(token, "portable", exe, info.SHA256["portable"]); err != nil {
		return err
	}
	step("Starting Manashakti Support")
	if err := exec.Command(exe).Start(); err != nil {
		return err
	}
	time.Sleep(8 * time.Second) // let the first start unpack before querying it
	id, err := waitForID(exe, 90*time.Second)
	if err != nil {
		return err
	}
	host, user := hostInfo()
	step("Telling your technician this computer is ready")
	if err := enroll(token, map[string]string{"mode": "help", "id": id, "hostname": host, "username": user}); err != nil {
		return err
	}
	fmt.Printf("\nReady. %s can connect now.\n", info.Technician)
	fmt.Println("When the Manashakti Support window asks, click Accept.")
	fmt.Println("Close the Manashakti Support window to end help at any time.")
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func serviceExists(name string) bool {
	out, _ := hiddenCommand("sc.exe", "query", name).Output()
	return bytes.Contains(out, []byte("SERVICE_NAME"))
}

// msiFailureContext returns the log lines leading up to the first failing action
// ("Return value 3"). msiexec writes the log as UTF-16LE or ANSI.
func msiFailureContext(raw []byte) string {
	text := string(raw)
	if len(raw) >= 2 && (raw[0] == 0xFF && raw[1] == 0xFE || bytes.Count(raw[:min(len(raw), 200)], []byte{0}) > 50) {
		u := make([]uint16, 0, len(raw)/2)
		for i := 0; i+1 < len(raw); i += 2 {
			u = append(u, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		text = string(utf16.Decode(u))
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	for i, l := range lines {
		if strings.Contains(l, "Return value 3") {
			from := max(0, i-12)
			var keep []string
			for _, c := range lines[from : i+1] {
				if c = strings.TrimSpace(strings.TrimPrefix(c, "\ufeff")); c != "" {
					if len(c) > 200 {
						c = c[:200]
					}
					keep = append(keep, "    "+c)
				}
			}
			return strings.Join(keep, "\n")
		}
	}
	return ""
}

func serviceRunning(name string) bool {
	out, _ := hiddenCommand("sc.exe", "query", name).Output()
	return bytes.Contains(out, []byte("RUNNING"))
}

func runSetup(token string, info *inviteInfo) error {
	if !info.AllowSetup {
		return errors.New("this invite does not offer permanent access, or it was already used")
	}
	exe := filepath.Join(os.Getenv("ProgramFiles"), appName, appName+".exe")
	if fileExists(exe) && serviceExists(appName) {
		// Already installed (e.g. the PC had it before): reinstalling the same MSI over a
		// running copy fails with 1603, and is not needed to enable permanent access.
		step("Manashakti Support is already installed; using it")
		if !serviceRunning(appName) {
			_ = hiddenCommand("sc.exe", "start", appName).Run()
		}
	} else {
		dir := filepath.Join(os.TempDir(), appName+"-Setup")
		msi := filepath.Join(dir, appName+".msi")
		step("Downloading Manashakti Support installer")
		if err := download(token, "msi", msi, info.SHA256["msi"]); err != nil {
			return err
		}
		step("Installing (this takes a minute)")
		logPath := filepath.Join(dir, "install.log")
		cmd := hiddenCommand("msiexec.exe", "/i", msi, "/qn", "/norestart", "/l*v", logPath)
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !(errors.As(err, &ee) && ee.ExitCode() == 3010) {
				detail := ""
				if raw, rerr := os.ReadFile(logPath); rerr == nil {
					detail = "\n" + msiFailureContext(raw)
				}
				return fmt.Errorf("installation failed (%v); log: %s%s", err, logPath, detail)
			}
		}
		if !fileExists(exe) {
			return fmt.Errorf("installed program not found at %s", exe)
		}
	}
	step("Waiting for the Manashakti Support service")
	deadline := time.Now().Add(90 * time.Second)
	for !serviceRunning(appName) {
		if time.Now().After(deadline) {
			return errors.New("the Manashakti Support service did not start")
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(5 * time.Second)

	step("Setting up access for Manashakti IT")
	if out, err := hiddenCommand(exe, "--option", "verification-method", "use-both-passwords").CombinedOutput(); err != nil {
		return fmt.Errorf("could not enable permanent access: %v %s", err, out)
	}
	pw, err := randomPassword(24)
	if err != nil {
		return err
	}
	out, _ := hiddenCommand(exe, "--password", pw).CombinedOutput()
	if !strings.Contains(string(out), "Done!") {
		return fmt.Errorf("could not set the access password: %s", strings.TrimSpace(string(out)))
	}
	id, err := waitForID(exe, 60*time.Second)
	if err != nil {
		return err
	}
	host, user := hostInfo()
	step("Adding this computer to Manashakti IT's list")
	if err := enroll(token, map[string]string{"mode": "setup", "id": id, "password": pw, "hostname": host, "username": user}); err != nil {
		return err
	}
	fmt.Printf("\nDone. %s can now help this computer at any time.\n", info.Technician)
	fmt.Println("A small Manashakti Support window appears whenever someone is connected.")
	fmt.Println("To remove access: Windows Settings > Apps > Manashakti Support > Uninstall.")
	return nil
}

func step(s string) { fmt.Printf("  - %s...\n", s) }

func main() {
	flagMode := flag.String("mode", "", "help or setup (normally taken from the file name)")
	flagToken := flag.String("token", "", "invite token (normally taken from the file name)")
	flag.Parse()

	err := run(*flagMode, *flagToken)
	if err != nil {
		fmt.Printf("\nSorry, something went wrong:\n  %v\n", err)
		fmt.Println("Please tell your technician what this message says.")
	}
	fmt.Print("\nPress Enter to close this window.")
	bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		os.Exit(1)
	}
}

func run(mode, token string) error {
	setTitle("Manashakti Support")
	fmt.Println("Manashakti IT Support")
	fmt.Println("=====================")
	if mode == "" || token == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if mode, token, err = parseInvite(exe); err != nil {
			return err
		}
	}
	if mode != "help" && mode != "setup" {
		return errors.New("unknown mode")
	}
	if mode == "setup" && !isAdmin() {
		fmt.Println("Windows will ask for permission to install. Click Yes.")
		if err := relaunchElevated([]string{"-mode", mode, "-token", token}); err != nil {
			return fmt.Errorf("permission was not given (%v); run the file again and click Yes", err)
		}
		os.Exit(0)
	}
	info, err := getInfo(token)
	if err != nil {
		return err
	}
	fmt.Printf("Invited by %s for %s.\n\n", info.Technician, info.Name)
	if mode == "help" {
		return runHelp(token, info)
	}
	return runSetup(token, info)
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	defaultControlURL  = "http://127.0.0.1:18776"
	defaultGatewayPort = "18774"
)

type grantEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Code      string    `json:"code"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"data"`
}

type outputStyle struct {
	reset  string
	bold   string
	green  string
	cyan   string
	yellow string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, http.DefaultClient))
}

func run(arguments []string, stdout, stderr io.Writer, client *http.Client) int {
	flags := flag.NewFlagSet("pairqr", flag.ContinueOnError)
	flags.SetOutput(stderr)
	origin := flags.String("origin", "", "Mobile Web Gateway origin; auto-detected when omitted")
	name := flags.String("name", "Mobile Web", "paired client display name")
	controlURL := flags.String("control-url", defaultControlURL, "loopback Auth Control origin")
	output := flags.String("output", "", "optional PNG output path")
	terminal := flags.Bool("terminal", true, "render the QR code in the terminal")
	terminalRender := flags.String("terminal-render", "large", "terminal QR style: large, compact, or small")
	terminalIndent := flags.Int("terminal-indent", 2, "spaces before each terminal QR row")
	printLink := flags.Bool("print-link", true, "print the full pairing link")
	printMetadata := flags.Bool("print-metadata", true, "print the pairing heading and security warning")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *terminalRender != "large" && *terminalRender != "compact" && *terminalRender != "small" {
		fmt.Fprintln(stderr, "--terminal-render must be large, compact, or small")
		return 2
	}
	if *terminalIndent < 0 || *terminalIndent > 40 {
		fmt.Fprintln(stderr, "--terminal-indent must be between 0 and 40")
		return 2
	}
	stdoutStyle := styleForWriter(stdout, *terminal)
	stderrStyle := styleForWriter(stderr, *terminal)

	resolvedOrigin := strings.TrimSpace(*origin)
	if resolvedOrigin == "" {
		lanIP, err := detectLANIPv4()
		if err != nil {
			fmt.Fprintf(stderr, "detect LAN address: %v; pass --origin explicitly\n", err)
			return 1
		}
		resolvedOrigin = "http://" + net.JoinHostPort(lanIP.String(), defaultGatewayPort)
	}
	parsedOrigin, err := parseOrigin(resolvedOrigin)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --origin: %v\n", err)
		return 2
	}
	parsedControlURL, err := parseOrigin(*controlURL)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --control-url: %v\n", err)
		return 2
	}
	if !isLoopbackHost(parsedControlURL.Hostname()) {
		fmt.Fprintln(stderr, "invalid --control-url: Auth Control must use a loopback host")
		return 2
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(stderr, "--name must not be empty")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grant, err := createPairingGrant(ctx, client, parsedControlURL, strings.TrimSpace(*name))
	if err != nil {
		fmt.Fprintf(stderr, "create pairing grant: %v\n", err)
		return 1
	}
	pairingLink := strings.TrimRight(parsedOrigin.String(), "/") + "/pair#code=" + url.QueryEscape(grant.Data.Code)

	if *printMetadata {
		fmt.Fprintf(stdout, "%s%sMobile Web pairing QR%s %s(expires %s)%s\n",
			stdoutStyle.bold, stdoutStyle.cyan, stdoutStyle.reset,
			stdoutStyle.yellow, grant.Data.ExpiresAt.Local().Format(time.RFC3339), stdoutStyle.reset)
	}
	if *printLink {
		fmt.Fprintf(stdout, "%sPairing link:%s\n%s%s%s\n",
			stdoutStyle.green, stdoutStyle.reset, stdoutStyle.cyan, pairingLink, stdoutStyle.reset)
	}

	if strings.TrimSpace(*output) != "" {
		path, err := writePNG(*output, pairingLink)
		if err != nil {
			fmt.Fprintf(stderr, "write QR PNG: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%sPNG:%s %s%s%s\n",
			stdoutStyle.green, stdoutStyle.reset, stdoutStyle.cyan, path, stdoutStyle.reset)
	}
	if *printMetadata {
		fmt.Fprintf(stderr, "%sThis QR contains a one-time credential. Do not upload or share it publicly.%s\n",
			stderrStyle.yellow, stderrStyle.reset)
	}
	if *terminal {
		fmt.Fprintln(stdout)
		if err := renderTerminalQR(stdout, pairingLink, *terminalRender, *terminalIndent); err != nil {
			fmt.Fprintf(stderr, "render terminal QR: %v\n", err)
			return 1
		}
	}
	return 0
}

func styleForWriter(writer io.Writer, terminalOutput bool) outputStyle {
	if !terminalOutput || os.Getenv("NO_COLOR") != "" {
		return outputStyle{}
	}
	file, ok := writer.(*os.File)
	if !ok {
		return outputStyle{}
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return outputStyle{}
	}
	return outputStyle{
		reset: "\x1b[0m", bold: "\x1b[1m", green: "\x1b[32m", cyan: "\x1b[36m", yellow: "\x1b[33m",
	}
}

func parseOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("expected an explicit http(s) origin")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return nil, errors.New("origin must not contain credentials, a path, query, or fragment")
	}
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func createPairingGrant(ctx context.Context, client *http.Client, controlURL *url.URL, name string) (grantEnvelope, error) {
	var envelope grantEnvelope
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return envelope, err
	}
	endpoint := strings.TrimRight(controlURL.String(), "/") + "/v1/auth-control/pairing-grants"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return envelope, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return envelope, err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("decode Auth Control response: %w", err)
	}
	if response.StatusCode != http.StatusCreated || !envelope.Success || envelope.Data.Code == "" {
		return envelope, fmt.Errorf("Auth Control returned HTTP %d", response.StatusCode)
	}
	return envelope, nil
}

func detectLANIPv4() (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	type candidate struct {
		ip    net.IP
		score int
	}
	var best candidate
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsPrivate() {
				continue
			}
			score := 100
			if networkInterface.Name == "en0" || networkInterface.Name == "en1" {
				score += 100
			}
			if strings.HasPrefix(networkInterface.Name, "utun") || strings.HasPrefix(networkInterface.Name, "bridge") {
				score -= 50
			}
			if best.ip == nil || score > best.score {
				best = candidate{ip: ip.To4(), score: score}
			}
		}
	}
	if best.ip == nil {
		return nil, errors.New("no active private IPv4 address found")
	}
	return best.ip, nil
}

func renderTerminalQR(writer io.Writer, content, renderMode string, indent int) error {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return err
	}
	bitmap := code.Bitmap()
	if renderMode == "small" {
		return renderSmallTerminalQR(writer, bitmap, indent)
	}
	if renderMode == "large" {
		return renderLargeTerminalQR(writer, bitmap, indent)
	}
	return renderCompactTerminalQR(writer, bitmap, indent)
}

func renderLargeTerminalQR(writer io.Writer, bitmap [][]bool, indent int) error {
	for row := 0; row < len(bitmap); row++ {
		if _, err := io.WriteString(writer, strings.Repeat(" ", indent)+"\x1b[30;107m"); err != nil {
			return err
		}
		for _, dark := range bitmap[row] {
			cell := "  "
			if dark {
				cell = "██"
			}
			if _, err := io.WriteString(writer, cell); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(writer, "\x1b[0m\n"); err != nil {
			return err
		}
	}
	return nil
}

func renderSmallTerminalQR(writer io.Writer, bitmap [][]bool, indent int) error {
	dotMask := [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	for row := 0; row < len(bitmap); row += 4 {
		if _, err := io.WriteString(writer, strings.Repeat(" ", indent)+"\x1b[30;107m"); err != nil {
			return err
		}
		for column := 0; column < len(bitmap[row]); column += 2 {
			cell := rune(0x2800)
			for rowOffset := 0; rowOffset < 4; rowOffset++ {
				for columnOffset := 0; columnOffset < 2; columnOffset++ {
					if row+rowOffset < len(bitmap) && column+columnOffset < len(bitmap[row+rowOffset]) && bitmap[row+rowOffset][column+columnOffset] {
						cell += dotMask[rowOffset][columnOffset]
					}
				}
			}
			if _, err := io.WriteString(writer, string(cell)); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(writer, "\x1b[0m\n"); err != nil {
			return err
		}
	}
	return nil
}

func renderCompactTerminalQR(writer io.Writer, bitmap [][]bool, indent int) error {
	for row := 0; row < len(bitmap); row += 2 {
		if _, err := io.WriteString(writer, strings.Repeat(" ", indent)+"\x1b[30;107m"); err != nil {
			return err
		}
		for column, topDark := range bitmap[row] {
			bottomDark := row+1 < len(bitmap) && bitmap[row+1][column]
			cell := " "
			switch {
			case topDark && bottomDark:
				cell = "\u2588"
			case topDark:
				cell = "\u2580"
			case bottomDark:
				cell = "\u2584"
			}
			if _, err := io.WriteString(writer, cell); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(writer, "\x1b[0m\n"); err != nil {
			return err
		}
	}
	return nil
}

func writePNG(output, content string) (string, error) {
	absolutePath, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return "", err
	}
	data, err := qrcode.Encode(content, qrcode.Medium, 512)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(absolutePath, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(absolutePath, 0o600); err != nil {
		return "", err
	}
	return absolutePath, nil
}

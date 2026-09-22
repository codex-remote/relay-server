package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"
)

func TestParseOrigin(t *testing.T) {
	for _, value := range []string{
		"192.168.1.5:18774",
		"http://192.168.1.5:18774/pair",
		"http://192.168.1.5:18774?code=secret",
		"http://user@192.168.1.5:18774",
	} {
		if _, err := parseOrigin(value); err == nil {
			t.Errorf("parseOrigin(%q) unexpectedly succeeded", value)
		}
	}
	parsed, err := parseOrigin("http://192.168.1.5:18774/")
	if err != nil || parsed.String() != "http://192.168.1.5:18774" {
		t.Fatalf("parseOrigin valid result = %v, %v", parsed, err)
	}
}

func TestLoopbackControlHost(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false", host)
		}
	}
	if isLoopbackHost("192.168.1.5") {
		t.Fatal("LAN address unexpectedly accepted as an Auth Control host")
	}
}

func TestCreatePairingGrant(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/auth-control/pairing-grants" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["name"] != "My iPhone" {
			t.Errorf("name = %q", body["name"])
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"code": "one-time-code", "expires_at": expiresAt},
		})
	}))
	defer server.Close()
	controlURL, err := parseOrigin(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	grant, err := createPairingGrant(context.Background(), server.Client(), controlURL, "My iPhone")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Data.Code != "one-time-code" || !grant.Data.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("grant = %+v", grant.Data)
	}
}

func TestWritePNGUsesPrivatePermissions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "pairing.png")
	written, err := writePNG(output, "http://192.168.1.5:18774/pair#code=secret")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(written)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("permissions = %o", permissions)
	}
	file, err := os.Open(written)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header := make([]byte, 8)
	if _, err := io.ReadFull(file, header); err != nil {
		t.Fatal(err)
	}
	if string(header) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("PNG header = %q", header)
	}
}

func TestRenderCompactTerminalQR(t *testing.T) {
	content := "http://192.168.1.5:18774/pair#code=secret"
	var output strings.Builder
	if err := renderTerminalQR(&output, content, "compact", 2); err != nil {
		t.Fatal(err)
	}
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	bitmapSize := len(code.Bitmap())
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != (bitmapSize+1)/2 {
		t.Fatalf("terminal QR lines = %d, want %d", len(lines), (bitmapSize+1)/2)
	}
	firstLine := strings.TrimSuffix(strings.TrimPrefix(lines[0], "  \x1b[30;107m"), "\x1b[0m")
	if width := utf8.RuneCountInString(firstLine); width != bitmapSize {
		t.Fatalf("terminal QR width = %d, want %d", width, bitmapSize)
	}
	if !strings.Contains(output.String(), "\u2580") || !strings.Contains(output.String(), "\u2584") {
		t.Fatal("terminal QR is missing compact half-block cells")
	}
}

func TestRenderLargeTerminalQRUsesSquareCells(t *testing.T) {
	content := "http://192.168.1.5:18774/pair#code=secret"
	var output strings.Builder
	if err := renderTerminalQR(&output, content, "large", 2); err != nil {
		t.Fatal(err)
	}
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	bitmapSize := len(code.Bitmap())
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != bitmapSize {
		t.Fatalf("terminal QR lines = %d, want %d", len(lines), bitmapSize)
	}
	firstLine := strings.TrimSuffix(strings.TrimPrefix(lines[0], "  \x1b[30;107m"), "\x1b[0m")
	if width := utf8.RuneCountInString(firstLine); width != bitmapSize*2 {
		t.Fatalf("terminal QR width = %d, want %d", width, bitmapSize*2)
	}
	if !strings.Contains(output.String(), "██") {
		t.Fatal("large terminal QR is missing full block cells")
	}
}

func TestRenderCameraTerminalQRUsesSolidBackgroundCells(t *testing.T) {
	content := "http://192.168.1.5:18774/pair#code=secret"
	var output strings.Builder
	if err := renderTerminalQR(&output, content, "camera", 1); err != nil {
		t.Fatal(err)
	}
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	bitmapSize := len(code.Bitmap()) + 2
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != (bitmapSize+1)/2 {
		t.Fatalf("camera QR lines = %d, want %d", len(lines), (bitmapSize+1)/2)
	}
	for _, expected := range []string{
		"\x1b[48;2;0;0;0m ",
		"\x1b[48;2;255;255;255m ",
		"\x1b[38;2;0;0;0;48;2;255;255;255m▀",
		"\x1b[38;2;255;255;255;48;2;0;0;0m▀",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("camera QR is missing ANSI cell %q", expected)
		}
	}
	if strings.Contains(output.String(), "█") {
		t.Fatal("camera QR must not use full-block glyphs that can leave font seams")
	}
}

func TestAddQuietZoneAddsLightModules(t *testing.T) {
	bitmap := [][]bool{{true, false}, {false, true}}
	got := addQuietZone(bitmap, 1)
	if len(got) != 4 || len(got[0]) != 4 || !got[1][1] || !got[2][2] {
		t.Fatalf("quiet-zone bitmap = %#v", got)
	}
	for _, point := range [][2]int{{0, 0}, {0, 3}, {3, 0}, {3, 3}} {
		if got[point[0]][point[1]] {
			t.Fatalf("quiet-zone corner %v is dark", point)
		}
	}
}

func TestRenderSmallTerminalQR(t *testing.T) {
	content := "http://192.168.1.5:18774/pair#code=secret"
	var output strings.Builder
	if err := renderTerminalQR(&output, content, "small", 3); err != nil {
		t.Fatal(err)
	}
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	bitmapSize := len(code.Bitmap())
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != (bitmapSize+3)/4 {
		t.Fatalf("small terminal QR lines = %d, want %d", len(lines), (bitmapSize+3)/4)
	}
	firstLine := strings.TrimSuffix(strings.TrimPrefix(lines[0], "   \x1b[30;107m"), "\x1b[0m")
	if width := utf8.RuneCountInString(firstLine); width != (bitmapSize+1)/2 {
		t.Fatalf("small terminal QR width = %d, want %d", width, (bitmapSize+1)/2)
	}
	hasBrailleDots := false
	for _, character := range output.String() {
		if character >= '\u2801' && character <= '\u28ff' {
			hasBrailleDots = true
			break
		}
	}
	if !hasBrailleDots {
		t.Fatal("small terminal QR is missing Braille cells")
	}
}

func TestOutputStyleHonorsTerminalAndNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if style := styleForWriter(io.Discard, true); style != (outputStyle{}) {
		t.Fatalf("non-terminal writer style = %+v", style)
	}
	if style := styleForWriter(os.Stdout, false); style != (outputStyle{}) {
		t.Fatalf("disabled terminal output style = %+v", style)
	}
	t.Setenv("NO_COLOR", "1")
	if style := styleForWriter(os.Stdout, true); style != (outputStyle{}) {
		t.Fatalf("NO_COLOR style = %+v", style)
	}
}

func TestRunCanSuppressCredentialMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"code":       "one-time-code",
				"expires_at": time.Now().Add(10 * time.Minute).UTC(),
			},
		})
	}))
	defer server.Close()

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := run([]string{
		"--origin=http://192.168.1.5:18774",
		"--control-url=" + server.URL,
		"--terminal=false",
		"--print-link=false",
		"--print-metadata=false",
	}, &stdout, &stderr, server.Client())
	if exitCode != 0 {
		t.Fatalf("run exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("suppressed output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

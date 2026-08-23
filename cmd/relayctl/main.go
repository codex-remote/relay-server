package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/coder/websocket"
)

const defaultAppURL = "ws://127.0.0.1:18765/ws/app"

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(arguments []string) int {
	if len(arguments) == 0 {
		printUsage()
		return 2
	}
	switch arguments[0] {
	case "projects":
		return projects(arguments[1:])
	case "profiles":
		return profiles(arguments[1:])
	case "threads":
		return threads(arguments[1:])
	case "thread":
		return thread(arguments[1:])
	case "turn":
		return turn(arguments[1:])
	case "watch":
		return watch(arguments[1:])
	case "pair":
		return pair(arguments[1:])
	case "auth-clients":
		return authClients(arguments[1:])
	case "revoke-client":
		return revokeClient(arguments[1:])
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", arguments[0])
		printUsage()
		return 2
	}
}

const defaultAuthControlURL = "http://127.0.0.1:18776"

func pair(arguments []string) int {
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	controlURL := flags.String("control-url", envOrDefault("AUTH_CONTROL_URL", defaultAuthControlURL), "loopback Auth Control URL")
	origin := flags.String("origin", "", "Mobile Web Gateway origin, for example http://192.168.1.20:18774")
	name := flags.String("name", "Mobile Web", "client display name")
	if flags.Parse(arguments) != nil {
		return 2
	}
	parsedOrigin, err := url.Parse(strings.TrimRight(strings.TrimSpace(*origin), "/"))
	if err != nil || (parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https") || parsedOrigin.Host == "" || parsedOrigin.Path != "" || parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" {
		fmt.Fprintln(os.Stderr, "origin must be an explicit http(s) origin without a path")
		return 2
	}
	var response struct {
		Data struct {
			Code      string    `json:"code"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"data"`
	}
	if err := controlRequest(http.MethodPost, *controlURL+"/v1/auth-control/pairing-grants", map[string]any{"name": *name}, &response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s/pair#code=%s\n", parsedOrigin.String(), url.QueryEscape(response.Data.Code))
	fmt.Fprintf(os.Stderr, "Pairing link expires at %s\n", response.Data.ExpiresAt.Local().Format(time.RFC3339))
	return 0
}

func authClients(arguments []string) int {
	flags := flag.NewFlagSet("auth-clients", flag.ContinueOnError)
	controlURL := flags.String("control-url", envOrDefault("AUTH_CONTROL_URL", defaultAuthControlURL), "loopback Auth Control URL")
	if flags.Parse(arguments) != nil {
		return 2
	}
	var response struct {
		Data struct {
			Items []struct {
				ID        string     `json:"client_id"`
				Name      string     `json:"name"`
				LastSeen  *time.Time `json:"last_seen_at"`
				RevokedAt *time.Time `json:"revoked_at"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := controlRequest(http.MethodGet, *controlURL+"/v1/auth-control/clients", nil, &response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, client := range response.Data.Items {
		state := "active"
		if client.RevokedAt != nil {
			state = "revoked"
		}
		lastSeen := "never"
		if client.LastSeen != nil {
			lastSeen = client.LastSeen.Local().Format(time.RFC3339)
		}
		fmt.Printf("%s\t%s\t%s\tlast_seen=%s\n", client.ID, state, client.Name, lastSeen)
	}
	return 0
}

func revokeClient(arguments []string) int {
	flags := flag.NewFlagSet("revoke-client", flag.ContinueOnError)
	controlURL := flags.String("control-url", envOrDefault("AUTH_CONTROL_URL", defaultAuthControlURL), "loopback Auth Control URL")
	clientID := flags.String("client", "", "client ID")
	if flags.Parse(arguments) != nil {
		return 2
	}
	if strings.TrimSpace(*clientID) == "" {
		fmt.Fprintln(os.Stderr, "client is required")
		return 2
	}
	endpoint := *controlURL + "/v1/auth-control/clients/" + url.PathEscape(*clientID) + "/revoke"
	if err := controlRequest(http.MethodPost, endpoint, map[string]any{}, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("revoked", *clientID)
	return 0
}

func controlRequest(method, endpoint string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Auth Control request failed: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Auth Control returned %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	if target != nil && len(data) > 0 {
		if err := json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("decode Auth Control response: %w", err)
		}
	}
	return nil
}

func profiles(arguments []string) int {
	flags := flag.NewFlagSet("profiles", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	projectID := flags.String("project", "", "project ID")
	if flags.Parse(arguments) != nil {
		return 2
	}
	if *projectID == "" {
		fmt.Fprintln(os.Stderr, "project is required")
		return 2
	}
	message, _ := protocol.NewMessage(protocol.TypeExecutionProfileList, protocol.NewID(), sender(), protocol.ExecutionProfileListPayload{ProjectID: *projectID})
	response, err := request(*url, message, protocol.TypeExecutionProfileSnapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	payload, err := protocol.PayloadAs[protocol.ExecutionProfileSnapshotPayload](response)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, profile := range payload.Profiles {
		defaultMarker := ""
		if profile.ID == payload.DefaultProfileID {
			defaultMarker = "\tdefault"
		}
		fmt.Printf("%s\tallowed=%t%s\t%s\n", profile.ID, profile.Allowed, defaultMarker, profile.Description)
	}
	return 0
}

func projects(arguments []string) int {
	flags := flag.NewFlagSet("projects", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	if flags.Parse(arguments) != nil {
		return 2
	}
	message, _ := protocol.NewMessage(protocol.TypeProjectList, protocol.NewID(), sender(), protocol.ProjectListPayload{})
	response, err := request(*url, message, protocol.TypeProjectSnapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	payload, err := protocol.PayloadAs[protocol.ProjectSnapshotPayload](response)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, project := range payload.Projects {
		fmt.Printf("%s\t%s\t%d threads\t%s\n", project.ID, project.Name, project.ThreadCount, project.Path)
	}
	return 0
}

func threads(arguments []string) int {
	flags := flag.NewFlagSet("threads", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	projectID := flags.String("project", "", "project ID")
	if flags.Parse(arguments) != nil {
		return 2
	}
	if *projectID == "" {
		fmt.Fprintln(os.Stderr, "project is required")
		return 2
	}
	message, _ := protocol.NewMessage(protocol.TypeThreadList, protocol.NewID(), sender(), protocol.ThreadListPayload{ProjectID: *projectID})
	response, err := request(*url, message, protocol.TypeThreadSnapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	payload, err := protocol.PayloadAs[protocol.ThreadSnapshotPayload](response)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, thread := range payload.Threads {
		fmt.Printf("%s\t%s\t%s\t%s\n", thread.ID, thread.Status, thread.UpdatedAt.Format(time.RFC3339), thread.Title)
	}
	return 0
}

func thread(arguments []string) int {
	flags := flag.NewFlagSet("thread", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	projectID := flags.String("project", "", "project ID")
	threadID := flags.String("thread", "", "Codex thread ID")
	if flags.Parse(arguments) != nil {
		return 2
	}
	if *projectID == "" || *threadID == "" {
		fmt.Fprintln(os.Stderr, "project and thread are required")
		return 2
	}
	message, _ := protocol.NewMessage(protocol.TypeThreadRead, protocol.NewID(), sender(), protocol.ThreadReadPayload{ProjectID: *projectID, ThreadID: *threadID})
	response, err := request(*url, message, protocol.TypeThreadDetail)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(data))
	return 0
}

func turn(arguments []string) int {
	flags := flag.NewFlagSet("turn", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	projectID := flags.String("project", "", "project ID")
	threadID := flags.String("thread", "", "existing Codex thread ID")
	prompt := flags.String("prompt", "", "development instruction")
	profileID := flags.String("profile", "", "Codex permission profile ID")
	if flags.Parse(arguments) != nil {
		return 2
	}
	if *prompt == "" && flags.NArg() > 0 {
		*prompt = strings.Join(flags.Args(), " ")
	}
	if *projectID == "" || strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "project and prompt are required")
		return 2
	}
	connection, err := dial(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer connection.Close(websocket.StatusNormalClosure, "turn finished")
	traceID := protocol.NewID()
	start, _ := protocol.NewMessage(protocol.TypeTurnStart, traceID, sender(), protocol.TurnStartPayload{
		ProjectID: *projectID, ThreadID: *threadID, Prompt: strings.TrimSpace(*prompt), PermissionProfileID: *profileID,
	})
	if err := writeMessage(context.Background(), connection, start); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := readMessages(ctx, connection)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	activeThreadID, activeTurnID := "", ""
	for {
		select {
		case received := <-incoming:
			if received.err != nil {
				fmt.Fprintln(os.Stderr, "Relay connection ended:", received.err)
				return 1
			}
			if received.message.TraceID != traceID && received.message.Type != protocol.TypeAgentStatus {
				continue
			}
			if received.message.Type == protocol.TypeTurnStarted {
				payload, _ := protocol.PayloadAs[protocol.TurnStartedPayload](received.message)
				activeThreadID, activeTurnID = payload.ThreadID, payload.TurnID
			}
			if terminal, code := printTurnEvent(received.message); terminal {
				return code
			}
		case <-signals:
			if activeThreadID == "" || activeTurnID == "" {
				fmt.Fprintln(os.Stderr, "turn has not started yet")
				continue
			}
			interrupt, _ := protocol.NewMessage(protocol.TypeTurnInterrupt, traceID, sender(), protocol.TurnInterruptPayload{ThreadID: activeThreadID, TurnID: activeTurnID})
			writeContext, writeCancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := writeMessage(writeContext, connection, interrupt)
			writeCancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, "interrupt failed:", err)
				return 1
			}
			fmt.Fprintln(os.Stderr, "[relayctl] interruption requested")
			signal.Stop(signals)
		}
	}
}

func watch(arguments []string) int {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	if flags.Parse(arguments) != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connection, err := dial(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer connection.Close(websocket.StatusNormalClosure, "watch finished")
	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintln(os.Stderr, "Relay connection ended:", err)
			return 1
		}
		var value any
		if json.Unmarshal(data, &value) == nil {
			formatted, _ := json.MarshalIndent(value, "", "  ")
			fmt.Println(string(formatted))
		}
	}
}

func request(url string, message protocol.Message, expectedType string) (protocol.Message, error) {
	connection, err := dial(url)
	if err != nil {
		return protocol.Message{}, err
	}
	defer connection.Close(websocket.StatusNormalClosure, "request finished")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := writeMessage(ctx, connection, message); err != nil {
		return protocol.Message{}, err
	}
	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			return protocol.Message{}, err
		}
		response, err := protocol.Decode(data)
		if err != nil {
			continue
		}
		if response.TraceID != message.TraceID {
			continue
		}
		if response.Type == protocol.TypeTurnRejected {
			payload, _ := protocol.PayloadAs[protocol.TurnRejectedPayload](response)
			return protocol.Message{}, fmt.Errorf("%s: %s", payload.Code, payload.Message)
		}
		if response.Type == expectedType {
			return response, nil
		}
	}
}

func dial(url string) (*websocket.Conn, error) {
	connection, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to Relay: %w", err)
	}
	connection.SetReadLimit(256 * 1024)
	return connection, nil
}

type receivedMessage struct {
	message protocol.Message
	err     error
}

func readMessages(ctx context.Context, connection *websocket.Conn) <-chan receivedMessage {
	messages := make(chan receivedMessage, 1)
	go func() {
		defer close(messages)
		for {
			_, data, err := connection.Read(ctx)
			if err != nil {
				messages <- receivedMessage{err: err}
				return
			}
			message, err := protocol.Decode(data)
			if err == nil {
				messages <- receivedMessage{message: message}
			}
		}
	}()
	return messages
}

func writeMessage(ctx context.Context, connection *websocket.Conn, message protocol.Message) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, data)
}

func printTurnEvent(message protocol.Message) (bool, int) {
	switch message.Type {
	case protocol.TypeTurnStarted:
		payload, _ := protocol.PayloadAs[protocol.TurnStartedPayload](message)
		fmt.Fprintf(os.Stderr, "[relayctl] thread=%s turn=%s\n", payload.ThreadID, payload.TurnID)
	case protocol.TypeTurnOutput:
		payload, _ := protocol.PayloadAs[protocol.TurnOutputPayload](message)
		if payload.Stream == "stderr" {
			fmt.Fprint(os.Stderr, payload.Text)
		} else {
			fmt.Fprint(os.Stdout, payload.Text)
		}
	case protocol.TypeTurnItemStarted:
		payload, _ := protocol.PayloadAs[protocol.TurnItemStartedPayload](message)
		fmt.Fprintf(os.Stderr, "[relayctl] item started sequence=%d id=%s type=%s\n", payload.Sequence, payload.Item.ID, payload.Item.Type)
	case protocol.TypeTurnItemDelta:
		payload, _ := protocol.PayloadAs[protocol.TurnItemDeltaPayload](message)
		fmt.Fprintf(os.Stderr, "[relayctl] item delta sequence=%d id=%s field=%s bytes=%d\n", payload.Sequence, payload.ItemID, payload.Field, len(payload.Delta))
	case protocol.TypeTurnItemDone:
		payload, _ := protocol.PayloadAs[protocol.TurnItemCompletedPayload](message)
		fmt.Fprintf(os.Stderr, "[relayctl] item completed sequence=%d id=%s type=%s\n", payload.Sequence, payload.Item.ID, payload.Item.Type)
	case protocol.TypeTurnCompleted:
		payload, _ := protocol.PayloadAs[protocol.TurnCompletedPayload](message)
		fmt.Fprintf(os.Stderr, "\n[relayctl] completed in %s; changed files: %d\n", time.Duration(payload.DurationMS)*time.Millisecond, len(payload.ChangedFiles))
		if payload.Summary != "" {
			fmt.Fprintf(os.Stdout, "\n%s\n", payload.Summary)
		}
		if payload.Diff != "" {
			fmt.Fprintf(os.Stdout, "\nGit diff:\n%s\n", payload.Diff)
		}
		return true, 0
	case protocol.TypeTurnFailed, protocol.TypeTurnRejected:
		fmt.Fprintf(os.Stderr, "\n[relayctl] %s: %s\n", message.Type, string(message.Payload))
		return true, 1
	case protocol.TypeTurnInterrupted:
		fmt.Fprintln(os.Stderr, "\n[relayctl] turn interrupted")
		return true, 130
	}
	return false, 0
}

func sender() protocol.Sender { return protocol.Sender{Kind: "user", ID: "relayctl"} }

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `AI Coding Remote Relay control client

Usage:
  relayctl projects [--url ws://HOST:18765/ws/app]
  relayctl profiles --project PROJECT_ID
  relayctl threads --project PROJECT_ID
  relayctl thread --project PROJECT_ID --thread THREAD_ID
  relayctl turn --project PROJECT_ID [--thread THREAD_ID] [--profile PROFILE_ID] --prompt TEXT
  relayctl watch
  relayctl pair --origin http://LAN_IP:18774 [--name "My iPhone"]
  relayctl auth-clients
  relayctl revoke-client --client CLIENT_ID

Commands:
  projects  List Git projects exposed by the Mac Agent
  profiles  List App Server permission profiles allowed for one project
  threads   List Codex sessions for one project
  thread    Read one Codex thread with its persisted turns and items
  turn      Start a new Codex turn or continue an existing thread
  watch          Print every Relay event as formatted JSON
  pair           Create a short-lived, one-time Mobile Web pairing link
  auth-clients   List paired Runtime clients through loopback Auth Control
  revoke-client  Revoke a paired client and all of its sessions`)
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/coder/websocket"
)

const defaultAppURL = "ws://127.0.0.1:8080/ws/app"

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(arguments []string) int {
	if len(arguments) == 0 {
		printUsage()
		return 2
	}
	switch arguments[0] {
	case "projects":
		return projects(arguments[1:])
	case "threads":
		return threads(arguments[1:])
	case "turn":
		return turn(arguments[1:])
	case "watch":
		return watch(arguments[1:])
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", arguments[0])
		printUsage()
		return 2
	}
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

func turn(arguments []string) int {
	flags := flag.NewFlagSet("turn", flag.ContinueOnError)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	projectID := flags.String("project", "", "project ID")
	threadID := flags.String("thread", "", "existing Codex thread ID")
	prompt := flags.String("prompt", "", "development instruction")
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
	start, _ := protocol.NewMessage(protocol.TypeTurnStart, traceID, sender(), protocol.TurnStartPayload{ProjectID: *projectID, ThreadID: *threadID, Prompt: strings.TrimSpace(*prompt)})
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
  relayctl projects [--url ws://HOST:8080/ws/app]
  relayctl threads --project PROJECT_ID
  relayctl turn --project PROJECT_ID [--thread THREAD_ID] --prompt TEXT
  relayctl watch

Commands:
  projects  List Git projects exposed by the Mac Agent
  threads   List Codex sessions for one project
  turn      Start a new Codex turn or continue an existing thread
  watch     Print every Relay event as formatted JSON`)
}

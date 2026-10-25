package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	eventBufferSize      = 1024
	minimumEventProtocol = 20
	// Protocol 21 starts lifecycle subscriptions at the current EventHub sequence.
	liveOnlyEventProtocol = 21
)

type eventSubscription struct {
	Type string `json:"type"`
}

type eventSubscribeRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params struct {
		Subscriptions []eventSubscription `json:"subscriptions"`
	} `json:"params"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type workspaceEvent struct {
	Event string `json:"event"`
	Data  struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"data"`
}

type sessionSnapshot struct {
	FocusedWorkspaceID string
	WorkspaceIDs       map[string]bool
}

// WorkspaceHooks receive reconciled Herdr workspace lifecycle state.
type WorkspaceHooks struct {
	// Focused records a workspace as the most recent one. It runs for the
	// focused workspace found at startup and for every focus event.
	Focused func(string) error
	// Closed removes a closed workspace from history.
	Closed func(string) error
	// Reconcile, when set, runs once after the subscription, with the IDs of
	// every workspace the server still has, before any event is applied. It lets
	// history drop workspaces that closed while no watcher was connected, such
	// as during a server restart.
	Reconcile func(present map[string]bool) error
}

// WatchWorkspaceEvents returns when the connection fails or the stream ends;
// a server closing an established stream is a normal shutdown and returns nil.
// It never reconnects: a restarted server needs a fresh watcher, so callers
// release their election and let a later lifecycle hook start one.
func WatchWorkspaceEvents(ctx context.Context, socketPath string, hooks WorkspaceHooks) error {
	if socketPath == "" {
		return errors.New("HERDR_SOCKET_PATH is required")
	}
	err := watchWorkspaceEvents(ctx, socketPath, hooks)
	// Cancellation closes in-flight connections, so report it rather than the
	// resulting read error.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func watchWorkspaceEvents(ctx context.Context, socketPath string, hooks WorkspaceHooks) error {
	// Protocol 21 does not replay, so start buffering before any other request.
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to Herdr event stream: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	request := eventSubscribeRequest{ID: "herdr-sesh-history", Method: "events.subscribe"}
	request.Params.Subscriptions = []eventSubscription{{Type: "workspace.focused"}, {Type: "workspace.closed"}}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return fmt.Errorf("subscribe to Herdr workspace events: %w", err)
	}

	decoder := json.NewDecoder(conn)
	var acknowledgement struct {
		Result struct {
			Type string `json:"type"`
		} `json:"result"`
		Error *apiError `json:"error"`
	}
	if err := decoder.Decode(&acknowledgement); err != nil {
		return fmt.Errorf("read Herdr event subscription acknowledgement: %w", err)
	}
	if acknowledgement.Error != nil {
		return fmt.Errorf("herdr event subscription failed: %s: %s", acknowledgement.Error.Code, acknowledgement.Error.Message)
	}
	if acknowledgement.Result.Type != "subscription_started" {
		return fmt.Errorf("unexpected Herdr event subscription acknowledgement %q", acknowledgement.Result.Type)
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	events := make(chan workspaceEvent, eventBufferSize)
	streamErrors := make(chan error, 1)
	go decodeWorkspaceEvents(streamCtx, decoder, events, streamErrors)

	protocol, err := loadServerProtocol(ctx, socketPath)
	if err != nil {
		return err
	}
	if protocol < minimumEventProtocol {
		return fmt.Errorf("workspace history requires Herdr protocol %d or newer; server uses protocol %d", minimumEventProtocol, protocol)
	}

	snapshot, err := loadSessionSnapshot(ctx, socketPath)
	if err != nil {
		return err
	}
	// Reconcile first so the prune runs against the same snapshot that gates
	// the buffered events, and cannot drop the focus recorded just below.
	if hooks.Reconcile != nil {
		if err := hooks.Reconcile(snapshot.WorkspaceIDs); err != nil {
			return err
		}
	}
	if snapshot.FocusedWorkspaceID != "" && hooks.Focused != nil {
		if err := hooks.Focused(snapshot.FocusedWorkspaceID); err != nil {
			return err
		}
	}
	applyEvents := func(events []workspaceEvent) error {
		if len(events) == 0 {
			return nil
		}
		var current *sessionSnapshot
		if protocol < liveOnlyEventProtocol {
			snapshot, err := loadSessionSnapshot(ctx, socketPath)
			if err != nil {
				return err
			}
			current = &snapshot
		}
		return applyWorkspaceEvents(events, current, hooks)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-streamErrors:
			if drainErr := applyEvents(drainBufferedEvents(events, nil)); drainErr != nil {
				return drainErr
			}
			return streamResult(err)
		case event := <-events:
			if err := applyEvents(drainBufferedEvents(events, []workspaceEvent{event})); err != nil {
				return err
			}
		}
	}
}

func decodeWorkspaceEvents(ctx context.Context, decoder *json.Decoder, events chan<- workspaceEvent, streamErrors chan<- error) {
	for {
		var event workspaceEvent
		if err := decoder.Decode(&event); err != nil {
			select {
			case streamErrors <- err:
			case <-ctx.Done():
			}
			return
		}
		if event.Data.WorkspaceID == "" || (event.Event != "workspace_focused" && event.Event != "workspace_closed") {
			continue
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return
		}
	}
}

func drainBufferedEvents(events <-chan workspaceEvent, buffered []workspaceEvent) []workspaceEvent {
	for {
		select {
		case event := <-events:
			buffered = append(buffered, event)
		default:
			return buffered
		}
	}
}

func applyWorkspaceEvents(events []workspaceEvent, snapshot *sessionSnapshot, hooks WorkspaceHooks) error {
	for _, event := range events {
		if snapshot != nil {
			exists := snapshot.WorkspaceIDs[event.Data.WorkspaceID]
			if (event.Event == "workspace_focused" && !exists) || (event.Event == "workspace_closed" && exists) {
				continue
			}
		}
		if err := applyWorkspaceEvent(event, hooks); err != nil {
			return err
		}
	}
	if snapshot != nil && snapshot.FocusedWorkspaceID != "" && hooks.Focused != nil {
		return hooks.Focused(snapshot.FocusedWorkspaceID)
	}
	return nil
}

func applyWorkspaceEvent(event workspaceEvent, hooks WorkspaceHooks) error {
	switch event.Event {
	case "workspace_focused":
		if hooks.Focused == nil {
			return nil
		}
		return hooks.Focused(event.Data.WorkspaceID)
	case "workspace_closed":
		if hooks.Closed == nil {
			return nil
		}
		return hooks.Closed(event.Data.WorkspaceID)
	default:
		return nil
	}
}

func streamResult(err error) error {
	// The decoder reports a close between messages as io.EOF and a close
	// mid-message as io.ErrUnexpectedEOF, so only the former is clean.
	if errors.Is(err, io.EOF) {
		return nil
	}
	return fmt.Errorf("read Herdr workspace event: %w", err)
}

func loadServerProtocol(ctx context.Context, socketPath string) (int, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return 0, fmt.Errorf("connect to Herdr server: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	request := struct {
		ID     string   `json:"id"`
		Method string   `json:"method"`
		Params struct{} `json:"params"`
	}{ID: "herdr-sesh-history-ping", Method: "ping"}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return 0, fmt.Errorf("ping Herdr server: %w", err)
	}
	var response struct {
		Result struct {
			Type     string `json:"type"`
			Protocol int    `json:"protocol"`
		} `json:"result"`
		Error *apiError `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return 0, fmt.Errorf("read Herdr ping response: %w", err)
	}
	if response.Error != nil {
		return 0, fmt.Errorf("herdr ping failed: %s: %s", response.Error.Code, response.Error.Message)
	}
	if response.Result.Type != "pong" {
		return 0, fmt.Errorf("unexpected Herdr ping response %q", response.Result.Type)
	}
	return response.Result.Protocol, nil
}

func loadSessionSnapshot(ctx context.Context, socketPath string) (sessionSnapshot, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return sessionSnapshot{}, fmt.Errorf("connect to Herdr session snapshot: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	request := struct {
		ID     string   `json:"id"`
		Method string   `json:"method"`
		Params struct{} `json:"params"`
	}{ID: "herdr-sesh-history-snapshot", Method: "session.snapshot"}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return sessionSnapshot{}, fmt.Errorf("request Herdr session snapshot: %w", err)
	}
	var response struct {
		Result struct {
			Type     string `json:"type"`
			Snapshot struct {
				FocusedWorkspaceID string `json:"focused_workspace_id"`
				Workspaces         []struct {
					WorkspaceID string `json:"workspace_id"`
				} `json:"workspaces"`
			} `json:"snapshot"`
		} `json:"result"`
		Error *apiError `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return sessionSnapshot{}, fmt.Errorf("read Herdr session snapshot: %w", err)
	}
	if response.Error != nil {
		return sessionSnapshot{}, fmt.Errorf("herdr session snapshot failed: %s: %s", response.Error.Code, response.Error.Message)
	}
	if response.Result.Type != "session_snapshot" {
		return sessionSnapshot{}, fmt.Errorf("unexpected Herdr session snapshot response %q", response.Result.Type)
	}
	snapshot := sessionSnapshot{
		FocusedWorkspaceID: response.Result.Snapshot.FocusedWorkspaceID,
		WorkspaceIDs:       make(map[string]bool, len(response.Result.Snapshot.Workspaces)),
	}
	for _, workspace := range response.Result.Snapshot.Workspaces {
		snapshot.WorkspaceIDs[workspace.WorkspaceID] = true
	}
	return snapshot, nil
}

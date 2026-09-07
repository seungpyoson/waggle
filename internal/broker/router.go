package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/seungpyoson/waggle/internal/brokerstate"
	"strconv"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Commands that work without a session handshake.
// Everything else requires connect first.
var noSessionRequired = map[string]bool{
	protocol.CmdEnroll: true, protocol.CmdRetire: true,
	protocol.CmdConnect: true, protocol.CmdStatus: true, protocol.CmdStop: true,
	protocol.CmdSend: true, protocol.CmdEnqueue: true, protocol.CmdInbox: true,
	protocol.CmdAck: true, protocol.CmdReply: true, protocol.CmdWhoami: true,
	protocol.CmdPresence: true, protocol.CmdConversationStop: true,
}

// route dispatches a request to the appropriate handler.
// Session check is enforced here once — individual handlers do not check.
func route(s *call, req protocol.Request) protocol.Response {
	if !noSessionRequired[req.Cmd] && s.name == "" {
		return protocol.ErrResponse(protocol.ErrNotConnected, "not connected")
	}

	switch req.Cmd {
	case protocol.CmdConnect:
		return handleConnect(s, req)
	case protocol.CmdDisconnect:
		return handleDisconnect(s)
	case protocol.CmdPublish:
		return handlePublish(s, req)
	case protocol.CmdSubscribe:
		return handleSubscribe(s, req)
	case protocol.CmdTaskCreate, protocol.CmdTaskList, protocol.CmdTaskClaim,
		protocol.CmdTaskComplete, protocol.CmdTaskFail, protocol.CmdTaskHeartbeat,
		protocol.CmdTaskCancel, protocol.CmdTaskGet, protocol.CmdTaskUpdate, protocol.CmdStatus:
		return routeTasks(s, req)
	case protocol.CmdLock:
		return handleLock(s, req)
	case protocol.CmdUnlock:
		return handleUnlock(s, req)
	case protocol.CmdLocks:
		return handleLocks(s)
	case protocol.CmdStop:
		return handleStop(s)
	case protocol.CmdSend, protocol.CmdEnqueue, protocol.CmdInbox, protocol.CmdAck,
		protocol.CmdReply, protocol.CmdWhoami, protocol.CmdPresence, protocol.CmdConversationStop, protocol.CmdEnroll, protocol.CmdRetire:
		return routeMessages(s, req)
	default:
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "unknown command")
	}
}

func handleConnect(s *call, req protocol.Request) protocol.Response {
	if req.Name == "" || len(req.Name) > config.Defaults.MaxFieldLength {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "connection name outside configured bounds")
	}
	if s.name != "" {
		return protocol.ErrResponse(protocol.ErrAlreadyConnected, "already connected")
	}
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	if _, exists := s.broker.sessions[req.Name]; exists {
		return protocol.ErrResponse(protocol.ErrAlreadyConnected, "connection name already in use")
	}
	s.name = req.Name
	s.broker.sessions[s.name] = s.Session
	return protocol.OKResponse(nil)
}

func handleDisconnect(s *call) protocol.Response {
	s.cleanDisconnect.Store(true)
	// Don't call cleanup() here — readLoop will return after encoding
	// this response (see cleanDisconnect check), triggering deferred cleanup.
	// Calling cleanup here closes the conn before the OK response is sent.
	return protocol.OKResponse(nil)
}

func handlePublish(s *call, req protocol.Request) protocol.Response {
	if req.Topic == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
	}

	// Validate JSON if message is provided
	if req.Message != "" && !json.Valid([]byte(req.Message)) {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "message must be valid JSON")
	}

	// Build event data
	var data json.RawMessage
	if req.Message != "" {
		data = json.RawMessage(req.Message)
	}

	evt := protocol.Event{
		Topic: req.Topic,
		Event: "custom",
		Data:  data,
		TS:    time.Now().UTC().Format(time.RFC3339),
	}
	s.broker.hub.Publish(req.Topic, mustMarshal(evt))
	return protocol.OKResponse(nil)
}

func handleSubscribe(s *call, req protocol.Request) protocol.Response {
	if req.Topic == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
	}

	ch := s.broker.hub.Subscribe(req.Topic, s.name)

	// Switch to streaming mode
	// Messages from the hub are already marshaled Event objects
	// Write them directly to the connection without wrapping
	s.streams.Add(1)
	go func() {
		defer s.streams.Done()
		for msg := range ch {
			// msg is already a marshaled Event, write it directly
			// CLASS 1 FIX (B1): Hold writeMu to prevent race with readLoop enc.Encode
			s.writeMu.Lock()
			s.conn.Write(msg)
			s.conn.Write([]byte("\n"))
			s.writeMu.Unlock()
		}
	}()

	return protocol.OKResponse(nil)
}

func handleTaskCreate(s *taskCall, req protocol.Request) protocol.Response {

	// Validate priority
	if req.Priority < 0 || req.Priority > config.Defaults.MaxPriority {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("priority must be between 0 and %d", config.Defaults.MaxPriority))
	}

	// Validate field lengths
	if len(req.Type) > config.Defaults.MaxFieldLength {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("type too long (max %d chars)", config.Defaults.MaxFieldLength))
	}
	if len(req.IdempotencyKey) > config.Defaults.MaxFieldLength {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("idempotency_key too long (max %d chars)", config.Defaults.MaxFieldLength))
	}

	// Parse tags
	var tags []string
	if req.Tags != "" {
		tags = strings.Split(req.Tags, ",")
	}

	// Parse depends_on
	var dependsOn []int64
	if req.DependsOn != "" {
		for _, idStr := range strings.Split(req.DependsOn, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil {
				return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid depends_on")
			}
			dependsOn = append(dependsOn, id)
		}
	}

	// Validate TTL
	if req.TTL < 0 {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "ttl must be non-negative")
	}
	if req.TTL > config.Defaults.MaxTaskTTL {
		return protocol.ErrResponse(protocol.ErrInvalidRequest,
			fmt.Sprintf("ttl exceeds maximum (%d seconds)", config.Defaults.MaxTaskTTL))
	}

	task, err := s.store.Create(tasks.CreateParams{
		IdempotencyKey: req.IdempotencyKey,
		Type:           req.Type,
		Tags:           tags,
		Payload:        string(req.Payload),
		Priority:       req.Priority,
		DependsOn:      dependsOn,
		LeaseDuration:  req.Lease,
		MaxRetries:     req.MaxRetries,
		TTL:            req.TTL,
	})
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Publish task.created event
	s.event("task.created", task)

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleTaskList(s *taskCall, req protocol.Request) protocol.Response {

	taskList, err := s.store.List(tasks.ListFilter{
		State: req.State,
		Type:  req.Type,
		Owner: req.Owner,
	})
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	data, _ := json.Marshal(taskList)
	return protocol.OKResponse(data)
}

func handleTaskClaim(s *taskCall, req protocol.Request) protocol.Response {

	var tags []string
	if req.Tags != "" {
		tags = strings.Split(req.Tags, ",")
	}

	task, err := s.store.Claim(s.name, tasks.ClaimFilter{
		Type: req.Type,
		Tags: tags,
	})
	if err != nil {
		if err.Error() == "no eligible task" {
			return protocol.ErrResponse(protocol.ErrNoEligibleTask, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Publish task.claimed event
	s.event("task.claimed", task)

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleTaskComplete(s *taskCall, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.store.Complete(taskID, req.ClaimToken, string(req.Result))
	if err != nil {
		if strings.Contains(err.Error(), "invalid claim token") {
			return protocol.ErrResponse(protocol.ErrInvalidToken, err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Get updated task and publish event
	task, err := s.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	s.event("task.completed", task)

	// Resolve dependencies
	unblocked, err := tasks.ResolveDeps(s.store, taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	for _, id := range unblocked {
		t, err := s.store.Get(id)
		if err != nil {
			return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
		}
		s.event("task.unblocked", t)
	}

	return protocol.OKResponse(nil)
}

func handleTaskFail(s *taskCall, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.store.Fail(taskID, req.ClaimToken, req.Reason)
	if err != nil {
		if strings.Contains(err.Error(), "invalid claim token") {
			return protocol.ErrResponse(protocol.ErrInvalidToken, err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Get updated task and publish event
	task, err := s.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	s.event("task.failed", task)

	// Fail dependents
	failed, err := tasks.FailDependents(s.store, taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	for _, id := range failed {
		t, err := s.store.Get(id)
		if err != nil {
			return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
		}
		s.event("task.failed", t)
	}

	return protocol.OKResponse(nil)
}

func handleTaskHeartbeat(s *taskCall, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.store.Heartbeat(taskID, req.ClaimToken)
	if err != nil {
		if strings.Contains(err.Error(), "invalid claim token") {
			return protocol.ErrResponse(protocol.ErrInvalidToken, err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	return protocol.OKResponse(nil)
}

func handleTaskCancel(s *taskCall, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.store.Cancel(taskID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Get updated task and publish event
	task, err := s.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	s.event("task.canceled", task)

	return protocol.OKResponse(nil)
}

func handleTaskGet(s *taskCall, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	task, err := s.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
	}

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleTaskUpdate(s *taskCall, req protocol.Request) protocol.Response {
	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	// Validate priority if provided
	if req.Priority != 0 && (req.Priority < 0 || req.Priority > config.Defaults.MaxPriority) {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("priority must be between 0 and %d", config.Defaults.MaxPriority))
	}

	var params tasks.UpdateParams

	// Parse priority if provided (non-zero means it was set)
	if req.Priority != 0 {
		params.Priority = &req.Priority
	}

	// Parse tags if provided
	if req.Tags != "" {
		params.Tags = strings.Split(req.Tags, ",")
		// Trim whitespace from each tag
		for i := range params.Tags {
			params.Tags[i] = strings.TrimSpace(params.Tags[i])
		}
	}

	// At least one field must be specified
	if params.Priority == nil && params.Tags == nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "at least one field must be specified")
	}

	err = s.store.Update(taskID, params)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
	}

	// Return updated task
	task, err := s.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
	}

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleLock(s *call, req protocol.Request) protocol.Response {
	if req.Resource == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "resource required")
	}

	err := s.broker.lockMgr.Acquire(req.Resource, s.name)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrResourceLocked, err.Error())
	}

	return protocol.OKResponse(nil)
}

func handleUnlock(s *call, req protocol.Request) protocol.Response {
	if req.Resource == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "resource required")
	}

	s.broker.lockMgr.Release(req.Resource, s.name)
	return protocol.OKResponse(nil)
}

func handleLocks(s *call) protocol.Response {

	locks := s.broker.lockMgr.List()
	data, _ := json.Marshal(locks)
	return protocol.OKResponse(data)
}

func handleStatus(s *taskCall) protocol.Response {
	s.broker.mu.RLock()
	sessionCount := len(s.broker.sessions)
	s.broker.mu.RUnlock()

	// Get task counts by state
	taskCounts, err := s.store.CountByState()
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, "failed to get task counts")
	}

	status := map[string]interface{}{
		"sessions":    sessionCount,
		"topics":      s.broker.hub.TopicCount(),
		"subscribers": s.broker.hub.SubscriberCount(),
		"locks":       s.broker.lockMgr.Count(),
		"tasks":       taskCounts,
	}

	// Add queue health
	health, err := s.store.QueueHealth(s.broker.config.TaskStaleThreshold)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	status["queue_health"] = health

	data, _ := json.Marshal(status)
	return protocol.OKResponse(data)
}

// handleStop records the operator's intent instead of draining inside the RPC.
// Draining closes ingress and every accepted connection with it, so beginning
// it here would destroy this connection before it could carry the
// acknowledgement. The read loop begins draining once the reply has been written.
func handleStop(s *call) protocol.Response {
	s.stopCause = fmt.Errorf("stop requested by %s", s.name)
	return protocol.OKResponse(nil)
}

func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustMarshal: %v", err))
	}
	return data
}

// call carries one admitted RPC lifetime through all of its transactions.
type call struct {
	*Session
	op *brokerstate.Operation
}

type taskCall struct {
	*call
	store   *tasks.Store
	effects []protocol.Event
}

func (s *taskCall) event(name string, task *tasks.Task) {
	s.effects = append(s.effects, protocol.Event{Topic: "task.events", Event: name, Data: mustMarshal(task), TS: time.Now().UTC().Format(time.RFC3339)})
}

type requestFailure struct{ response protocol.Response }

func (e *requestFailure) Error() string { return e.response.Error }

func routeTasks(s *call, req protocol.Request) protocol.Response {
	var response protocol.Response
	var effects []protocol.Event
	err := s.op.Write(context.Background(), func(tx *brokerstate.WriteTx) error {
		task := &taskCall{call: s, store: tasks.NewStore(tx)}
		switch req.Cmd {
		case protocol.CmdTaskCreate:
			response = handleTaskCreate(task, req)
		case protocol.CmdTaskList:
			response = handleTaskList(task, req)
		case protocol.CmdTaskClaim:
			response = handleTaskClaim(task, req)
		case protocol.CmdTaskComplete:
			response = handleTaskComplete(task, req)
		case protocol.CmdTaskFail:
			response = handleTaskFail(task, req)
		case protocol.CmdTaskHeartbeat:
			response = handleTaskHeartbeat(task, req)
		case protocol.CmdTaskCancel:
			response = handleTaskCancel(task, req)
		case protocol.CmdTaskGet:
			response = handleTaskGet(task, req)
		case protocol.CmdTaskUpdate:
			response = handleTaskUpdate(task, req)
		case protocol.CmdStatus:
			response = handleStatus(task)
		default:
			return fmt.Errorf("invalid task command %s", req.Cmd)
		}
		if !response.OK {
			return &requestFailure{response}
		}
		effects = task.effects
		return nil
	})
	if err != nil {
		var rejected *requestFailure
		if errors.As(err, &rejected) {
			return rejected.response
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	for _, event := range effects {
		s.broker.hub.Publish(event.Topic, mustMarshal(event))
	}
	return response
}

package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seungpyoson/waggle/internal/config"
	"github.com/seungpyoson/waggle/internal/messages"
	"github.com/seungpyoson/waggle/internal/protocol"
	"github.com/seungpyoson/waggle/internal/tasks"
)

// Commands that work without a session handshake.
// Everything else requires connect first.
var noSessionRequired = map[string]bool{
	protocol.CmdConnect: true,
	protocol.CmdStatus:  true,
	protocol.CmdStop:    true,
}

// route dispatches a request to the appropriate handler.
// Session check is enforced here once — individual handlers do not check.
func route(s *Session, req protocol.Request) protocol.Response {
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
	case protocol.CmdTaskCreate:
		return handleTaskCreate(s, req)
	case protocol.CmdTaskList:
		return handleTaskList(s, req)
	case protocol.CmdTaskClaim:
		return handleTaskClaim(s, req)
	case protocol.CmdTaskComplete:
		return handleTaskComplete(s, req)
	case protocol.CmdTaskFail:
		return handleTaskFail(s, req)
	case protocol.CmdTaskHeartbeat:
		return handleTaskHeartbeat(s, req)
	case protocol.CmdTaskCancel:
		return handleTaskCancel(s, req)
	case protocol.CmdTaskGet:
		return handleTaskGet(s, req)
	case protocol.CmdTaskUpdate:
		return handleTaskUpdate(s, req)
	case protocol.CmdLock:
		return handleLock(s, req)
	case protocol.CmdUnlock:
		return handleUnlock(s, req)
	case protocol.CmdLocks:
		return handleLocks(s)
	case protocol.CmdStatus:
		return handleStatus(s)
	case protocol.CmdStop:
		return handleStop(s)
	case protocol.CmdSend:
		return handleSend(s, req)
	case protocol.CmdInbox:
		return handleInbox(s, req)
	case protocol.CmdAck:
		return handleAck(s, req)
	case protocol.CmdPresence:
		return handlePresence(s)
	case protocol.CmdSpawnRegister:
		return handleSpawnRegister(s, req)
	case protocol.CmdSpawnUpdatePID:
		return handleSpawnUpdatePID(s, req)
	default:
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "unknown command")
	}
}

func handleConnect(s *Session, req protocol.Request) protocol.Response {
	if req.Name == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "name required")
	}
	if len(req.Name) > config.Defaults.MaxFieldLength {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("name too long (max %d chars)", config.Defaults.MaxFieldLength))
	}
	if s.name != "" {
		return protocol.ErrResponse(protocol.ErrAlreadyConnected, "already connected")
	}

	s.broker.mu.Lock()
	if existing, ok := s.broker.sessions[req.Name]; ok && existing != s {
		s.broker.mu.Unlock()
		return protocol.ErrResponse(protocol.ErrAlreadyConnected, "session name already in use")
	}
	s.name = req.Name
	s.broker.sessions[s.name] = s
	s.broker.mu.Unlock()

	// Publish presence.online event
	publishPresenceEvent(s.broker, "presence.online", s.name)

	return protocol.OKResponse(nil)
}

func handleDisconnect(s *Session) protocol.Response {
	s.cleanDisconnect = true
	// Don't call cleanup() here — readLoop will return after encoding
	// this response (see cleanDisconnect check), triggering deferred cleanup.
	// Calling cleanup here closes the conn before the OK response is sent.
	return protocol.OKResponse(nil)
}

func handlePublish(s *Session, req protocol.Request) protocol.Response {
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

func handleSubscribe(s *Session, req protocol.Request) protocol.Response {
	if req.Topic == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
	}

	ch := s.broker.hub.Subscribe(req.Topic, s.name)

	// Switch to streaming mode
	// Messages from the hub are already marshaled Event objects
	// Write them directly to the connection without wrapping
	go func() {
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

func handleTaskCreate(s *Session, req protocol.Request) protocol.Response {

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

	task, err := s.broker.store.Create(tasks.CreateParams{
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
	publishTaskEvent(s.broker, "task.created", task)

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleTaskList(s *Session, req protocol.Request) protocol.Response {

	taskList, err := s.broker.store.List(tasks.ListFilter{
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

func handleTaskClaim(s *Session, req protocol.Request) protocol.Response {

	var tags []string
	if req.Tags != "" {
		tags = strings.Split(req.Tags, ",")
	}

	task, err := s.broker.store.Claim(s.name, tasks.ClaimFilter{
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
	publishTaskEvent(s.broker, "task.claimed", task)

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleTaskComplete(s *Session, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.broker.store.Complete(taskID, req.ClaimToken, string(req.Result))
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
	task, _ := s.broker.store.Get(taskID)
	publishTaskEvent(s.broker, "task.completed", task)

	// Resolve dependencies
	unblocked, _ := tasks.ResolveDeps(s.broker.store, taskID)
	for _, id := range unblocked {
		t, _ := s.broker.store.Get(id)
		publishTaskEvent(s.broker, "task.unblocked", t)
	}

	return protocol.OKResponse(nil)
}

func handleTaskFail(s *Session, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.broker.store.Fail(taskID, req.ClaimToken, req.Reason)
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
	task, _ := s.broker.store.Get(taskID)
	publishTaskEvent(s.broker, "task.failed", task)

	// Fail dependents
	failed, _ := tasks.FailDependents(s.broker.store, taskID)
	for _, id := range failed {
		t, _ := s.broker.store.Get(id)
		publishTaskEvent(s.broker, "task.failed", t)
	}

	return protocol.OKResponse(nil)
}

func handleTaskHeartbeat(s *Session, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.broker.store.Heartbeat(taskID, req.ClaimToken)
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

func handleTaskCancel(s *Session, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	err = s.broker.store.Cancel(taskID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Get updated task and publish event
	task, _ := s.broker.store.Get(taskID)
	publishTaskEvent(s.broker, "task.canceled", task)

	return protocol.OKResponse(nil)
}

func handleTaskGet(s *Session, req protocol.Request) protocol.Response {

	taskID, err := strconv.ParseInt(req.TaskID, 10, 64)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "invalid task_id")
	}

	task, err := s.broker.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
	}

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleTaskUpdate(s *Session, req protocol.Request) protocol.Response {
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

	err = s.broker.store.Update(taskID, params)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
	}

	// Return updated task
	task, err := s.broker.store.Get(taskID)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
	}

	data, _ := json.Marshal(task)
	return protocol.OKResponse(data)
}

func handleLock(s *Session, req protocol.Request) protocol.Response {
	if req.Resource == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "resource required")
	}

	err := s.broker.lockMgr.Acquire(req.Resource, s.name)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrResourceLocked, err.Error())
	}

	return protocol.OKResponse(nil)
}

func handleUnlock(s *Session, req protocol.Request) protocol.Response {
	if req.Resource == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "resource required")
	}

	s.broker.lockMgr.Release(req.Resource, s.name)
	return protocol.OKResponse(nil)
}

func handleLocks(s *Session) protocol.Response {

	locks := s.broker.lockMgr.List()
	data, _ := json.Marshal(locks)
	return protocol.OKResponse(data)
}

func handleStatus(s *Session) protocol.Response {
	s.broker.mu.RLock()
	sessionCount := len(s.broker.sessions)
	s.broker.mu.RUnlock()

	// Get task counts by state
	taskCounts, err := s.broker.store.CountByState()
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, "failed to get task counts")
	}

	status := map[string]interface{}{
		"sessions":    sessionCount,
		"topics":      s.broker.hub.TopicCount(),
		"subscribers": s.broker.hub.SubscriberCount(),
		"locks":       s.broker.lockMgr.Count(),
		"tasks":       taskCounts,
		"spawned":     s.broker.spawnMgr.List(),
	}

	// Add queue health
	health, err := s.broker.store.QueueHealth(s.broker.config.TaskStaleThreshold)
	if err == nil {
		status["queue_health"] = health
	}

	data, _ := json.Marshal(status)
	return protocol.OKResponse(data)
}

func handleStop(s *Session) protocol.Response {
	go s.broker.Shutdown()
	return protocol.OKResponse(nil)
}

// publishTaskEvent publishes a task event to the task.events topic
func publishTaskEvent(b *Broker, event string, task *tasks.Task) {
	if task == nil {
		return
	}

	evt := protocol.Event{
		Topic: "task.events",
		Event: event,
		Data:  mustMarshal(task),
		TS:    time.Now().UTC().Format(time.RFC3339),
	}

	b.hub.Publish("task.events", mustMarshal(evt))
}

func mustMarshal(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func handleSend(s *Session, req protocol.Request) protocol.Response {
	// CLASS 3 FIX (G3): Validate recipient name length
	if req.Name == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "recipient name required")
	}
	if len(req.Name) > config.Defaults.MaxFieldLength {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("recipient name too long (max %d chars)", config.Defaults.MaxFieldLength))
	}

	// CLASS 3 FIX (G2): Validate message body size
	if req.Message == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "message required")
	}
	if len(req.Message) > int(config.Defaults.MaxMessageSize) {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("message body too large (max %d bytes)", config.Defaults.MaxMessageSize))
	}

	// Parse priority (default to normal if empty)
	priority := req.MsgPriority
	if priority == "" {
		priority = config.Defaults.DefaultMsgPriority
	}

	// Parse TTL (nil when 0)
	var ttl *int
	if req.TTL > 0 {
		ttl = &req.TTL
	}

	// Send message
	msg, err := s.broker.msgStore.Send(s.name, req.Name, req.Message, priority, ttl)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Handle --await-ack: Register waiter BEFORE push to avoid race
	// If ack arrives between push and waiter registration, sender would timeout
	var ackCh chan struct{}
	if req.AwaitAck {
		ackCh = make(chan struct{}, 1) // buffered: ack can arrive before select
		s.broker.ackWaitersMu.Lock()
		s.broker.ackWaiters[msg.ID] = ackCh
		s.broker.ackWaitersMu.Unlock()
	}

	// Push to recipient if connected
	s.broker.mu.RLock()
	recipient, online := s.broker.sessions[req.Name]
	s.broker.mu.RUnlock()

	// CLASS 2 FIX (B3): Skip push delivery when sending to self to prevent protocol corruption
	if online && recipient != s {
		pushMsg := protocol.Response{
			OK: true,
			Data: mustMarshal(map[string]any{
				"type":    "message",
				"id":      msg.ID,
				"from":    msg.From,
				"body":    msg.Body,
				"sent_at": msg.CreatedAt,
			}),
		}
		recipient.writeMu.Lock()
		// CLASS 1 FIX (B2): Check encode error before marking as pushed
		if err := recipient.enc.Encode(pushMsg); err != nil {
			recipient.writeMu.Unlock()
			// Don't mark as pushed if delivery failed
			log.Printf("session %s: failed to push message %d to %s: %v", s.name, msg.ID, req.Name, err)
		} else {
			recipient.writeMu.Unlock()
			if err := s.broker.msgStore.MarkPushed(msg.ID); err != nil {
				log.Printf("session %s: failed to mark message %d as pushed: %v", s.name, msg.ID, err)
			}
		}
	}

	// Also push to persistent listener (<name>-push) if connected
	pushName := req.Name + "-push"
	s.broker.mu.RLock()
	pushRecipient, pushOnline := s.broker.sessions[pushName]
	s.broker.mu.RUnlock()

	if pushOnline && pushRecipient != s {
		pushResp := protocol.Response{
			OK: true,
			Data: mustMarshal(map[string]any{
				"type":    "message",
				"id":      msg.ID,
				"from":    msg.From,
				"body":    msg.Body,
				"sent_at": msg.CreatedAt,
			}),
		}
		pushRecipient.writeMu.Lock()
		if err := pushRecipient.enc.Encode(pushResp); err != nil {
			pushRecipient.writeMu.Unlock()
			log.Printf("session %s: failed to push message %d to %s: %v", s.name, msg.ID, pushName, err)
		} else {
			pushRecipient.writeMu.Unlock()
		}
	}

	// Wait for ack if requested
	if req.AwaitAck {
		timeout := time.Duration(req.Timeout) * time.Second
		if timeout <= 0 {
			timeout = config.Defaults.AwaitAckDefaultTimeout
		}

		select {
		case <-ackCh:
			return protocol.OKResponse(mustMarshal(msg))
		case <-time.After(timeout):
			s.broker.ackWaitersMu.Lock()
			delete(s.broker.ackWaiters, msg.ID) // no leak
			s.broker.ackWaitersMu.Unlock()
			return protocol.ErrResponse(protocol.ErrTimeout, "await-ack timed out")
		case <-s.broker.stopCh:
			s.broker.ackWaitersMu.Lock()
			delete(s.broker.ackWaiters, msg.ID)
			s.broker.ackWaitersMu.Unlock()
			return protocol.ErrResponse(protocol.ErrInternalError, "broker shutting down")
		}
	}

	return protocol.OKResponse(mustMarshal(msg))
}

func handleInbox(s *Session, req protocol.Request) protocol.Response {
	messages, err := s.broker.msgStore.Inbox(s.name)
	if err != nil {
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}
	return protocol.OKResponse(mustMarshal(messages))
}

func handleAck(s *Session, req protocol.Request) protocol.Response {
	if req.MessageID == 0 {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "message_id required")
	}

	err := s.broker.msgStore.Ack(req.MessageID, s.name)
	if err != nil {
		if errors.Is(err, messages.ErrMessageNotFound) {
			return protocol.ErrResponse(protocol.ErrMessageNotFound, err.Error())
		}
		if errors.Is(err, messages.ErrNotRecipient) {
			return protocol.ErrResponse(protocol.ErrForbidden, err.Error())
		}
		return protocol.ErrResponse(protocol.ErrInternalError, err.Error())
	}

	// Signal --await-ack sender if blocked
	s.broker.ackWaitersMu.Lock()
	if ch, ok := s.broker.ackWaiters[req.MessageID]; ok {
		delete(s.broker.ackWaiters, req.MessageID)
		s.broker.ackWaitersMu.Unlock()
		close(ch) // buffered ch, never blocks
	} else {
		s.broker.ackWaitersMu.Unlock()
	}

	return protocol.OKResponse(nil)
}

func handlePresence(s *Session) protocol.Response {
	s.broker.mu.RLock()
	// Collect all raw session names first
	allNames := make(map[string]bool, len(s.broker.sessions))
	for name := range s.broker.sessions {
		allNames[name] = true
	}
	// Build presence list, hiding -push listeners only when the base agent exists
	seen := make(map[string]bool)
	agents := make([]map[string]string, 0, len(s.broker.sessions))
	for name := range s.broker.sessions {
		displayName := name
		if base := strings.TrimSuffix(name, "-push"); base != name {
			// This is a -push listener. Hide it if the base agent session exists.
			if allNames[base] {
				continue
			}
			// Base agent not connected — show the -push listener under the base name
			// so the agent is still discoverable.
			displayName = base
		}
		if seen[displayName] {
			continue
		}
		seen[displayName] = true
		agents = append(agents, map[string]string{"name": displayName, "state": "online"})
	}
	s.broker.mu.RUnlock()

	sort.Slice(agents, func(i, j int) bool { return agents[i]["name"] < agents[j]["name"] })

	return protocol.OKResponse(mustMarshal(agents))
}

// publishPresenceEvent publishes a presence event to the presence.events topic
func publishPresenceEvent(b *Broker, event, name string) {
	evt := protocol.Event{
		Topic: "presence.events",
		Event: event,
		Data:  mustMarshal(map[string]string{"name": name}),
		TS:    time.Now().UTC().Format(time.RFC3339),
	}
	b.hub.Publish("presence.events", mustMarshal(evt))
}

func handleSpawnRegister(s *Session, req protocol.Request) protocol.Response {
	if req.Name == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "name required")
	}

	// Parse PID and type from Payload
	var spawnData struct {
		PID  int    `json:"pid"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(req.Payload, &spawnData); err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("invalid spawn data: %v", err))
	}
	// Allow PID=0 as a fallback when PID detection fails (macOS limitation)
	if spawnData.PID < 0 {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "valid pid required")
	}

	if err := s.broker.spawnMgr.Add(req.Name, spawnData.Type, spawnData.PID); err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, err.Error())
	}

	return protocol.OKResponse(nil)
}

func handleSpawnUpdatePID(s *Session, req protocol.Request) protocol.Response {
	if req.Name == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "name required")
	}

	var data struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(req.Payload, &data); err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, fmt.Sprintf("invalid data: %v", err))
	}

	if err := s.broker.spawnMgr.UpdatePID(req.Name, data.PID); err != nil {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, err.Error())
	}

	return protocol.OKResponse(nil)
}


package broker

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

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
	default:
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "unknown command")
	}
}

func handleConnect(s *Session, req protocol.Request) protocol.Response {
	if req.Name == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "name required")
	}
	if s.name != "" {
		return protocol.ErrResponse(protocol.ErrAlreadyConnected, "already connected")
	}

	s.name = req.Name
	s.broker.mu.Lock()
	s.broker.sessions[s.name] = s
	s.broker.mu.Unlock()

	return protocol.OKResponse(nil)
}

func handleDisconnect(s *Session) protocol.Response {
	s.cleanDisconnect = true
	s.cleanup()
	return protocol.OKResponse(nil)
}

func handlePublish(s *Session, req protocol.Request) protocol.Response {
	if req.Topic == "" {
		return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
	}
	s.broker.hub.Publish(req.Topic, []byte(req.Message))
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
			s.conn.Write(msg)
			s.conn.Write([]byte("\n"))
		}
	}()

	return protocol.OKResponse(nil)
}

func handleTaskCreate(s *Session, req protocol.Request) protocol.Response {

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

	task, err := s.broker.store.Create(tasks.CreateParams{
		IdempotencyKey: req.IdempotencyKey,
		Type:           req.Type,
		Tags:           tags,
		Payload:        string(req.Payload),
		Priority:       req.Priority,
		DependsOn:      dependsOn,
		LeaseDuration:  req.Lease,
		MaxRetries:     req.MaxRetries,
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


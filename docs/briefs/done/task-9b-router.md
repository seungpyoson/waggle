# Task 9b: Router — Command Dispatch

**Branch:** `feat/9-broker-core` (same branch as 9a)
**File:** `internal/broker/router.go`
**Depends on:** Task 9a (session)

## What to build

A single function that dispatches requests to the right module. This is the central wiring point.

```go
func route(b *Broker, s *Session, req protocol.Request) protocol.Response
```

**The complete switch statement (implement ALL cases):**

```go
func route(b *Broker, s *Session, req protocol.Request) protocol.Response {
    switch req.Cmd {

    case protocol.CmdConnect:
        if s.name != "" {
            return protocol.ErrResponse(protocol.ErrAlreadyConnected, "already connected as "+s.name)
        }
        if req.Name == "" {
            return protocol.ErrResponse(protocol.ErrInvalidRequest, "name required")
        }
        s.name = req.Name
        b.addSession(s)
        slog.Info("session connected", "name", s.name)
        return protocol.OKResponse(mustJSON(map[string]string{"name": s.name}))

    case protocol.CmdDisconnect:
        s.cleanDisconnect = true
        slog.Info("clean disconnect", "name", s.name)
        return protocol.OKResponse(nil)

    case protocol.CmdPublish:
        if req.Topic == "" {
            return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
        }
        b.hub.Publish(req.Topic, []byte(req.Message))
        return protocol.OKResponse(nil)

    case protocol.CmdSubscribe:
        if req.Topic == "" {
            return protocol.ErrResponse(protocol.ErrInvalidRequest, "topic required")
        }
        // Returns OK, then session switches to streaming mode
        // The actual subscribe + streaming is handled by session.readLoop after this returns
        return protocol.OKResponse(mustJSON(map[string]string{"topic": req.Topic}))

    case protocol.CmdTaskCreate:
        // Validate deps if provided
        if len(req.DependsOn) > 0 {
            if err := tasks.ValidateDeps(b.store, req.DependsOn, 0); err != nil {
                return protocol.ErrResponse(protocol.ErrInvalidRequest, err.Error())
            }
        }
        params := tasks.CreateParams{
            Payload:        string(req.Payload),
            Type:           req.Type,
            Tags:           req.Tags,
            DependsOn:      req.DependsOn,
            IdempotencyKey: req.IdempotencyKey,
        }
        if req.Priority != nil {
            params.Priority = *req.Priority
        }
        if req.MaxRetries != nil {
            params.MaxRetries = *req.MaxRetries
        }
        // Parse lease duration string if provided (e.g., "10m", "1h")
        task, err := b.store.Create(params)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrInternal, err.Error())
        }
        publishTaskEvent(b, "task.created", task)
        return protocol.OKResponse(mustJSON(task))

    case protocol.CmdTaskClaim:
        filter := tasks.ClaimFilter{Type: req.Type, Tags: req.Tags}
        task, err := b.store.Claim(s.name, filter)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrNoEligibleTask, err.Error())
        }
        s.claimedTasks = append(s.claimedTasks, task.ID)
        publishTaskEvent(b, "task.claimed", task)
        return protocol.OKResponse(mustJSON(task))

    case protocol.CmdTaskComplete:
        err := b.store.Complete(req.TaskID, req.ClaimToken, req.Result)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrInvalidToken, err.Error())
        }
        task, _ := b.store.Get(req.TaskID)
        publishTaskEvent(b, "task.completed", task)
        // Check if this completion unblocks anything
        unblocked, _ := tasks.ResolveDeps(b.store, req.TaskID)
        for _, id := range unblocked {
            if t, err := b.store.Get(id); err == nil {
                publishTaskEvent(b, "task.unblocked", t)
            }
        }
        return protocol.OKResponse(mustJSON(task))

    case protocol.CmdTaskFail:
        err := b.store.Fail(req.TaskID, req.ClaimToken, req.Reason)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrInvalidToken, err.Error())
        }
        task, _ := b.store.Get(req.TaskID)
        publishTaskEvent(b, "task.failed", task)
        // Fail dependents
        failed, _ := tasks.FailDependents(b.store, req.TaskID)
        for _, id := range failed {
            if t, err := b.store.Get(id); err == nil {
                publishTaskEvent(b, "task.failed", t)
            }
        }
        return protocol.OKResponse(mustJSON(task))

    case protocol.CmdTaskHeartbeat:
        err := b.store.Heartbeat(req.TaskID, req.ClaimToken)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrInvalidToken, err.Error())
        }
        return protocol.OKResponse(nil)

    case protocol.CmdTaskCancel:
        err := b.store.Cancel(req.TaskID)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
        }
        task, _ := b.store.Get(req.TaskID)
        publishTaskEvent(b, "task.canceled", task)
        // Fail dependents of canceled task
        failed, _ := tasks.FailDependents(b.store, req.TaskID)
        for _, id := range failed {
            if t, err := b.store.Get(id); err == nil {
                publishTaskEvent(b, "task.failed", t)
            }
        }
        return protocol.OKResponse(mustJSON(task))

    case protocol.CmdTaskGet:
        task, err := b.store.Get(req.TaskID)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrTaskNotFound, err.Error())
        }
        return protocol.OKResponse(mustJSON(task))

    case protocol.CmdTaskList:
        filter := tasks.ListFilter{State: req.State, Type: req.Type, Owner: req.Owner}
        list, err := b.store.List(filter)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrInternal, err.Error())
        }
        return protocol.OKResponse(mustJSON(list))

    case protocol.CmdTaskUpdate:
        // Future: append progress note to task
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "task.update not yet implemented")

    case protocol.CmdLock:
        if req.Resource == "" {
            return protocol.ErrResponse(protocol.ErrInvalidRequest, "resource required")
        }
        err := b.locks.Acquire(req.Resource, s.name)
        if err != nil {
            return protocol.ErrResponse(protocol.ErrResourceLocked, err.Error())
        }
        return protocol.OKResponse(nil)

    case protocol.CmdUnlock:
        b.locks.Release(req.Resource, s.name)
        return protocol.OKResponse(nil)

    case protocol.CmdLocks:
        return protocol.OKResponse(mustJSON(b.locks.List()))

    case protocol.CmdStatus:
        status := map[string]any{
            "sessions":    b.SessionCount(),
            "topics":      b.hub.TopicCount(),
            "subscribers": b.hub.SubscriberCount(),
            "locks":       b.locks.Count(),
        }
        // Add task stats
        all, _ := b.store.List(tasks.ListFilter{})
        stats := map[string]int{}
        for _, t := range all {
            stats[t.State]++
        }
        status["tasks"] = stats
        return protocol.OKResponse(mustJSON(status))

    case protocol.CmdStop:
        go b.Shutdown() // async — let response send first
        return protocol.OKResponse(nil)

    default:
        return protocol.ErrResponse(protocol.ErrInvalidRequest, "unknown command: "+req.Cmd)
    }
}

// mustJSON marshals v to json.RawMessage. Panics on error (should never happen with known types).
func mustJSON(v any) json.RawMessage {
    data, err := json.Marshal(v)
    if err != nil {
        panic("json marshal: " + err.Error())
    }
    return data
}

// publishTaskEvent publishes a state change to the task.events topic.
func publishTaskEvent(b *Broker, eventName string, task *tasks.Task) {
    data, _ := json.Marshal(map[string]any{
        "topic": "task.events",
        "event": eventName,
        "id":    task.ID,
        "type":  task.Type,
        "state": task.State,
        "by":    task.ClaimedBy,
        "ts":    time.Now().UTC().Format(time.RFC3339),
    })
    b.hub.Publish("task.events", data)
}
```

## What the implementer must do

1. Copy the route function above into `router.go`
2. Adjust import paths to match the actual package layout
3. Add the `mustJSON` and `publishTaskEvent` helpers
4. Verify it compiles against the actual Store/Hub/Manager/Session types
5. Fix any type mismatches between this reference code and the actual implementations from Tasks 3-7

## Tests

Router is tested via broker integration tests (Task 9c). Each route case is covered by a `TestBroker_*` test.

## Acceptance criteria

- [ ] All protocol.Cmd* constants have a case in the switch
- [ ] Default case returns error (no silent drops)
- [ ] Every task state change calls publishTaskEvent
- [ ] task.complete triggers ResolveDeps
- [ ] task.fail and task.cancel trigger FailDependents
- [ ] Input validation on required fields (topic, resource, name)
- [ ] Commit: `feat(broker): router — command dispatch`

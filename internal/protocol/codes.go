package protocol

// Command constants — the `cmd` field values in Request
const (
	CmdConnect  = "connect"
	CmdDisconnect = "disconnect"
	CmdPublish  = "publish"
	CmdSubscribe = "subscribe"

	CmdTaskCreate    = "task.create"
	CmdTaskList      = "task.list"
	CmdTaskClaim     = "task.claim"
	CmdTaskComplete  = "task.complete"
	CmdTaskFail      = "task.fail"
	CmdTaskHeartbeat = "task.heartbeat"
	CmdTaskCancel    = "task.cancel"
	CmdTaskGet       = "task.get"
	CmdTaskUpdate    = "task.update"

	CmdLock   = "lock"
	CmdUnlock = "unlock"
	CmdLocks  = "locks"
	CmdStatus = "status"
	CmdStop   = "stop"

	CmdSend     = "send"
	CmdInbox    = "inbox"
	CmdAck      = "ack"
	CmdPresence = "presence"

	CmdSpawnRegister = "spawn.register"
)

// Error code constants — the `code` field values in Response
const (
	ErrBrokerNotRunning         = "BROKER_NOT_RUNNING"
	ErrAlreadyConnected         = "ALREADY_CONNECTED"
	ErrNotConnected             = "NOT_CONNECTED"
	ErrResourceLocked           = "RESOURCE_LOCKED"
	ErrTaskNotFound             = "TASK_NOT_FOUND"
	ErrInvalidToken             = "INVALID_TOKEN"
	ErrNoEligibleTask           = "NO_ELIGIBLE_TASK"
	ErrInvalidRequest           = "INVALID_REQUEST"
	ErrDuplicateIdempotencyKey  = "DUPLICATE_IDEMPOTENCY_KEY"
	ErrInternalError            = "INTERNAL_ERROR"
	ErrMessageNotFound          = "MESSAGE_NOT_FOUND"
	ErrForbidden                = "FORBIDDEN"
	ErrTimeout                  = "TIMEOUT"
)


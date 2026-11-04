package mtprotoedge

import "github.com/owpengram/owpengram-td/mt"

const (
	rpcWorkerBusyErrorCode    = 500
	rpcWorkerBusyErrorMessage = "WORKER_BUSY_TOO_LONG_RETRY"
)

// rpcWorkerBusyError reports transient server admission pressure. A local
// worker/queue/ledger ceiling is not Telegram flood control: returning a 420
// FLOOD_WAIT would falsely blame the account and makes clients surface or cache
// a rate-limit state. Official clients classify 5xx as transient; TDLib and
// DrKlo additionally recognize WORKER_BUSY_TOO_LONG_RETRY and retry with delay.
func rpcWorkerBusyError() *mt.RPCError {
	return &mt.RPCError{
		ErrorCode:    rpcWorkerBusyErrorCode,
		ErrorMessage: rpcWorkerBusyErrorMessage,
	}
}

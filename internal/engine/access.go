// Who may write a node. The values live on Node.WriteAccess; enforcement is
// the store's job, because only the store knows who a request acts as.

package engine

// WriteAccess values. The zero value means unrestricted, so an old graph.yaml
// that has never heard of write access keeps behaving as it always did.
const (
	WriteAccessAll          = ""
	WriteAccessWorker       = "worker"
	WriteAccessOrchestrator = "orchestrator"
	WriteAccessHumanOnly    = "human-only"
)

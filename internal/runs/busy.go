package runs

import (
	"context"
	"database/sql"

	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// Lineage identifies runs that belong to the same lineage and never block each
// other in Layer 1 busy checks (§C.2.1).
type Lineage struct {
	PipelineID string
	WorkRoot   string
	GroupID    string
	ContestID  string
}

// BusyQuery describes the node and/or repository a starting run would hold.
type BusyQuery struct {
	ActorID string
	IsAdmin bool
	Project string
	NodeID  string
	RepoKey string
	Global  bool
	Exclude Lineage
}

// Holder is one unfinished run currently holding a node or repository.
type Holder struct {
	Run     Record
	Hidden  bool
	Waiting bool
}

// BusyRequest is the input to applyBusy inside a start transaction (§C.2.3).
type BusyRequest struct {
	Query      BusyQuery
	OnBusy     string
	HolderIDs  []string
	RunnerCaps []string
	Group      bool
}

// BusyOutcome is the result of applyBusy.
type BusyOutcome struct {
	Proceed  bool
	AfterRun string
	Choice   string
	Stop     []Holder
	Audit    *AuditEntry
}

// applyBusy checks for active holders inside tx. Stub in Wave 0 (F1): proceed
// with nothing to do. Filled by C1.
func (d *Dispatcher) applyBusy(ctx context.Context, tx *sql.Tx, br BusyRequest) (BusyOutcome, error) {
	return BusyOutcome{Proceed: true}, nil
}

// afterBusy stops superseded holders and records audit after commit.
// Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) afterBusy(ctx context.Context, actor identity.Actor, o BusyOutcome) {}

// continueBusy delegates onBusy=="continue" to a follow-up before any tx.
// Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) continueBusy(ctx context.Context, req GlobalStartRequest) (Record, error) {
	return Record{}, refuse(StartInvalid, "continue is not yet available", "接續功能尚未啟用")
}

// waitsBehind reports whether a queued busy run must still wait for an active
// or older holder. Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) waitsBehind(ctx context.Context, r Record) bool { return false }

// heldByOther reports whether a queued writing pipeline/handoff run is held by
// another lineage. Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) heldByOther(ctx context.Context, r Record) bool { return false }

// coordJob adds carry-over context and sets Job.Coordinated at offer time.
// Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) coordJob(ctx context.Context, r Record, job *runnerwire.Job) {}

// busyEnded releases waiting runs and board claims when a run ends.
// Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) busyEnded(ctx context.Context, r Record) {}

// releaseWaiting pumps runners with queued busy runs whose wait has ended.
// Stub in Wave 0 (F1); filled by C1.
func (d *Dispatcher) releaseWaiting(ctx context.Context) {}

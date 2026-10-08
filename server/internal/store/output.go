package store

import "ant/server/internal/domain"

// OutputChunkRecord is a durable response fragment. Data is never copied into
// lifecycle events; those contain only the reference and sequence range.
type OutputChunkRecord struct {
	AttemptID domain.ID
	Sequence  int64
	Data      []byte
	SHA256    string
	CreatedAt domain.Millis
}

// OutputWriterTx keeps append, visibility, and the output_stored event in one
// serialized SQLite transaction.
type OutputWriterTx interface {
	PreparationWriterTx
	GetOutputChunk(attemptID domain.ID, sequence int64) (OutputChunkRecord, bool, error)
	CountOperationOutput(key domain.OperationKey) (chunks int64, bytes int64, err error)
	AppendOutputChunk(key domain.OperationKey, rec OutputChunkRecord) error
	MarkOutputVisible(key domain.OperationKey, at domain.Millis) error
}

// OutputReaderTx is an additive read projection. Callers authorize the
// operation namespace before reading any body bytes.
type OutputReaderTx interface {
	ListOperationOutput(key domain.OperationKey, afterSequence int64, limit int) ([]OutputChunkRecord, error)
	CountOperationOutput(key domain.OperationKey) (chunks int64, bytes int64, err error)
}

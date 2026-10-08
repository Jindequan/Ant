package commands

import (
	"context"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

var ErrControlSignalsInvalid = errors.New("ledger control signals: invalid input")

// ReadControlSignals returns durable cancel intents for active operations
// owned by this process. The runtime filter makes a bounded query sufficient:
// an operation claimed by another process is recovered by lease sweep.
func (s *PreparationService) ReadControlSignals(ctx context.Context, auth domain.AuthContext, namespace domain.ID, limit int) ([]ledger.ControlSignal, error) {
	if s == nil || s.st == nil || s.runtimeID == "" || namespace == "" ||
		auth.PrincipalID == "" || auth.Namespace != namespace || !hasPermission(auth.Permissions, domain.PermExecute) ||
		limit < 1 || limit > 128 {
		return nil, ErrControlSignalsInvalid
	}
	var signals []ledger.ControlSignal
	err := s.st.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.ControlSignalReaderTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		records, err := reader.ListRuntimeControlSignals(namespace, s.runtimeID, limit)
		if err != nil {
			return err
		}
		signals = make([]ledger.ControlSignal, 0, len(records))
		for _, rec := range records {
			if rec.Namespace != namespace || rec.OperationID == "" || !rec.CancelRequested || rec.ControlVersion < 1 {
				return ErrClaimIntegrity
			}
			signals = append(signals, ledger.ControlSignal{
				Key:             domain.OperationKey{Namespace: rec.Namespace, OperationID: rec.OperationID},
				CancelRequested: rec.CancelRequested, ControlVersion: rec.ControlVersion,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return signals, nil
}

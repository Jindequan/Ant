package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
)

type cancelCommandStub struct{ err error }

func (s cancelCommandStub) RequestCancel(context.Context, domain.AuthContext, domain.OperationKey, domain.ID) (domain.CancellationAck, error) {
	return domain.CancellationAck{}, s.err
}

type reconcileCommandStub struct {
	auth  domain.AuthContext
	input domain.ReconciliationInput
	view  domain.ReconciliationView
}

func (s *reconcileCommandStub) Reconcile(_ context.Context, auth domain.AuthContext, input domain.ReconciliationInput) (domain.ReconciliationView, error) {
	s.auth, s.input = auth, input
	return s.view, nil
}

func TestOperationReconciliationRequiresManageAndEvidence(t *testing.T) {
	stub := &reconcileCommandStub{view: domain.ReconciliationView{ReconciliationID: "rec-1", ConfirmedOutcome: "known_success"}}
	service, err := NewOperationReconciliations(stub)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ReconciliationInput{CommandID: "cmd-1", AttemptID: "att-1", ConfirmedOutcome: "known_success", RetrySafety: domain.Unsafe, EvidenceRefs: []domain.ID{"evidence-1"}}
	manage := domain.AuthContext{PrincipalID: "manager", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermManage}}
	view, err := service.Reconcile(context.Background(), manage, input)
	if err != nil || view.ReconciliationID != "rec-1" || stub.auth.Namespace != manage.Namespace || !reflect.DeepEqual(stub.input, input) {
		t.Fatalf("view=%+v err=%v auth=%+v input=%+v", view, err, stub.auth, stub.input)
	}
	if _, err := service.Reconcile(context.Background(), domain.AuthContext{PrincipalID: "reader", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermRead}}, input); !errors.Is(err, ErrOperationReconcileForbidden) {
		t.Fatalf("read-only reconciliation error=%v", err)
	}
	invalid := input
	invalid.EvidenceRefs = nil
	if _, err := service.Reconcile(context.Background(), manage, invalid); !errors.Is(err, ErrOperationReconcileInvalid) {
		t.Fatalf("missing evidence error=%v", err)
	}
}

func TestOperationControlsMapsCancelErrors(t *testing.T) {
	cases := []struct {
		name  string
		input error
		want  error
	}{
		{name: "forbidden", input: commands.ErrCancelForbidden, want: ErrOperationCancelForbidden},
		{name: "invalid", input: commands.ErrCancelInvalid, want: ErrOperationCancelInvalid},
		{name: "conflict", input: commands.ErrCancelConflict, want: ErrOperationCancelConflict},
		{name: "store failure is retained", input: errors.New("store unavailable"), want: errors.New("store unavailable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, err := NewOperationControls(cancelCommandStub{err: tc.input})
			if err != nil {
				t.Fatal(err)
			}
			_, got := service.RequestCancel(context.Background(), domain.AuthContext{}, domain.OperationKey{Namespace: "ns", OperationID: "op"}, "cmd")
			if tc.name == "store failure is retained" {
				if got == nil || got.Error() != tc.want.Error() {
					t.Fatalf("error=%v, want %v", got, tc.want)
				}
			} else if !errors.Is(got, tc.want) {
				t.Fatalf("error=%v, want %v", got, tc.want)
			}
		})
	}
}

// Package hooks defines bounded, explicit ANT lifecycle extension points.
// Hooks are registered by the composition root; they are not dynamically loaded.
// Guards can only veto, candidate orderers can only permute eligible IDs, and
// observers receive committed immutable events. Explicit Transformer points
// alter copied stage data only when the core validates every intermediate value.
package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

type Point string

// RegistryAPIVersion identifies the callback event and manifest contract.
// Bump it when the Hook API semantics change incompatibly.
const RegistryAPIVersion = "v1"

// Capability declares which hook contracts a lifecycle point supports. The
// composition root may define additional named points before sealing the
// registry; the core must still invoke the point at the intended boundary.
type Capability uint8

const (
	CapabilityGuard Capability = 1 << iota
	CapabilityOrderer
	CapabilityObserver
	CapabilityTransformer
)

const allCapabilities = CapabilityGuard | CapabilityOrderer | CapabilityObserver | CapabilityTransformer

const (
	DefaultMaxCustomPoints          = 64
	DefaultMaxRegistrationsPerPoint = 32
	DefaultMaxTransformBytes        = 1 << 20
	DefaultMaxEventPayloadBytes     = 1 << 20
)

const (
	PointRequestValidated   Point = "request.validated"
	PointCandidatesReady    Point = "candidates.ready"
	PointCandidatePrepare   Point = "candidate.before_prepare"
	PointAttemptAuthorize   Point = "attempt.before_authorize"
	PointDispatchBeforeSend Point = "dispatch.before_send"
	PointReportBeforeSettle Point = "report.before_settle"
	PointOperationClaimed   Point = "operation.claimed"
	PointDecisionCommitted  Point = "decision.committed"
	PointAttemptPrepared    Point = "attempt.prepared.committed"
	PointAttemptSettled     Point = "attempt.settled.committed"
	PointOperationCommitted Point = "operation.committed"
	PointConfigCommitted    Point = "config.committed"
)

var (
	ErrUnknownPoint               = errors.New("hooks: unknown point")
	ErrUnsupportedCapability      = errors.New("hooks: unsupported capability at point")
	ErrDuplicatePoint             = errors.New("hooks: duplicate point")
	ErrInvalidPoint               = errors.New("hooks: invalid point definition")
	ErrObserverRequiresPostCommit = errors.New("hooks: observer point must be post-commit")
	ErrPostCommitObserverOnly     = errors.New("hooks: post-commit point supports observers only")
	ErrDuplicateName              = errors.New("hooks: duplicate registration name")
	ErrInvalidRegistration        = errors.New("hooks: invalid registration")
	ErrGuardDenied                = errors.New("hooks: guard denied")
	ErrInvalidPermutation         = errors.New("hooks: invalid candidate permutation")
	ErrDuplicateCandidateID       = errors.New("hooks: duplicate candidate ID")
	ErrDeadlineRequired           = errors.New("hooks: a deadline is required")
	ErrInvalidContext             = errors.New("hooks: context is required")
	ErrHookFailed                 = errors.New("hooks: hook execution failed")
	ErrObserverFailed             = errors.New("hooks: observer failed")
	ErrLimitExceeded              = errors.New("hooks: registry limit exceeded")
	ErrInvalidOptions             = errors.New("hooks: invalid registry options")
	ErrInvalidTransformation      = errors.New("hooks: invalid transformation")
	ErrInvalidValidator           = errors.New("hooks: transformation validator is required")
	ErrTransformTooLarge          = errors.New("hooks: transformation payload exceeds limit")
	ErrEventTooLarge              = errors.New("hooks: event payload exceeds limit")
	ErrInvalidObserverDispatcher  = errors.New("hooks: invalid observer dispatcher")
	ErrObserverDispatcherClosed   = errors.New("hooks: observer dispatcher closed")
	ErrObserverQueueFull          = errors.New("hooks: observer queue full")
)

var builtInPoints = map[Point]Capability{
	PointRequestValidated:   CapabilityGuard,
	PointCandidatesReady:    CapabilityOrderer,
	PointCandidatePrepare:   CapabilityGuard,
	PointAttemptAuthorize:   CapabilityGuard,
	PointDispatchBeforeSend: CapabilityGuard,
	PointReportBeforeSettle: CapabilityGuard,
	PointOperationClaimed:   CapabilityObserver,
	PointDecisionCommitted:  CapabilityObserver,
	PointAttemptPrepared:    CapabilityObserver,
	PointAttemptSettled:     CapabilityObserver,
	PointOperationCommitted: CapabilityObserver,
	PointConfigCommitted:    CapabilityObserver,
}

// KnownPoint reports whether point belongs to the built-in lifecycle schema.
// A Registry may additionally contain points declared by its composition root.
func KnownPoint(point Point) bool {
	_, ok := builtInPoints[point]
	return ok
}

type Event struct {
	APIVersion       string
	RegistryRevision string
	Point            Point
	OperationID      string
	AttemptID        string
	ConfigRevision   string
	RuleRevision     string
	OccurredAtUnixMs int64
	Payload          []byte // redacted minimal snapshot; Registry enforces MaxEventPayloadBytes
}

func (e Event) clone() Event {
	e.Payload = slices.Clone(e.Payload)
	return e
}

type GuardDecision struct {
	Allowed bool
	Code    string
	Message string
}

type Guard func(context.Context, Event) GuardDecision
type Observer func(context.Context, Event) error
type CandidateOrderer func(context.Context, Event, []string) ([]string, error)
type Transformer func(context.Context, Event, []byte) ([]byte, error)
type TransformValidator func([]byte) error

type Registration struct {
	Name        string
	Version     string
	Point       Point
	Priority    int
	Guard       Guard
	Observer    Observer
	Orderer     CandidateOrderer
	Transformer Transformer
}

// PointManifestEntry describes the capabilities frozen for a lifecycle point.
type PointManifestEntry struct {
	Point        Point      `json:"point"`
	Capabilities Capability `json:"capabilities"`
	PostCommit   bool       `json:"postCommit"`
}

// HookManifestEntry describes a registered callback without exposing its code.
type HookManifestEntry struct {
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	Point      Point      `json:"point"`
	Priority   int        `json:"priority"`
	Capability Capability `json:"capability"`
}

// RegistryManifestLimits captures the effective resource limits that can
// change which registered callbacks run for an event.
type RegistryManifestLimits struct {
	MaxCustomPoints          int `json:"maxCustomPoints"`
	MaxRegistrationsPerPoint int `json:"maxRegistrationsPerPoint"`
	MaxTransformBytes        int `json:"maxTransformBytes"`
	MaxEventPayloadBytes     int `json:"maxEventPayloadBytes"`
}

// RegistryManifest is the deterministic, callback-free description of a sealed registry.
type RegistryManifest struct {
	APIVersion    string                 `json:"apiVersion"`
	Limits        RegistryManifestLimits `json:"limits"`
	Points        []PointManifestEntry   `json:"points"`
	Registrations []HookManifestEntry    `json:"registrations"`
}

type hook struct{ Registration }

// RegistryOptions bounds custom lifecycle seams, callbacks per seam, event
// snapshots, and transformation payload bytes. Zero disables the corresponding
// extension surface (and permits only empty event payloads); negative values
// are invalid.
type RegistryOptions struct {
	MaxCustomPoints          int
	MaxRegistrationsPerPoint int
	MaxTransformBytes        int
	MaxEventPayloadBytes     int
}

// DefaultRegistryOptions returns the standard extension budgets.
func DefaultRegistryOptions() RegistryOptions {
	return RegistryOptions{
		MaxCustomPoints:          DefaultMaxCustomPoints,
		MaxRegistrationsPerPoint: DefaultMaxRegistrationsPerPoint,
		MaxTransformBytes:        DefaultMaxTransformBytes,
		MaxEventPayloadBytes:     DefaultMaxEventPayloadBytes,
	}
}

type Registry struct {
	mu           sync.RWMutex
	sealed       bool
	capabilities map[Point]Capability
	postCommit   map[Point]struct{}
	byPoint      map[Point][]hook
	names        map[string]struct{}
	options      RegistryOptions
	manifest     RegistryManifest
	revision     string
}

func NewRegistry() *Registry {
	r, _ := NewRegistryWithOptions(DefaultRegistryOptions())
	return r
}

// NewRegistryWithOptions creates a registry with explicit extension budgets.
// Option values are used as-is; use DefaultRegistryOptions to start from the
// standard budget and override only selected fields.
func NewRegistryWithOptions(options RegistryOptions) (*Registry, error) {
	if options.MaxCustomPoints < 0 || options.MaxRegistrationsPerPoint < 0 || options.MaxTransformBytes < 0 || options.MaxEventPayloadBytes < 0 {
		return nil, ErrInvalidOptions
	}
	capabilities := make(map[Point]Capability, len(builtInPoints))
	postCommit := make(map[Point]struct{})
	for point, capability := range builtInPoints {
		capabilities[point] = capability
		if capability&CapabilityObserver != 0 {
			postCommit[point] = struct{}{}
		}
	}
	return &Registry{
		capabilities: capabilities,
		postCommit:   postCommit,
		byPoint:      make(map[Point][]hook),
		names:        make(map[string]struct{}),
		options:      options,
	}, nil
}

// DefinePoint adds a pre-commit lifecycle seam with typed decision or
// transformation contracts. Observer seams must use DefinePostCommitPoint.
// It is available only before Seal and is intended for trusted code compiled
// into the ANT composition root, not dynamic plugin metadata.
func (r *Registry) DefinePoint(point Point, capabilities Capability) error {
	if r == nil || !safeToken(string(point)) || capabilities == 0 || capabilities&^allCapabilities != 0 {
		return ErrInvalidPoint
	}
	if capabilities&CapabilityObserver != 0 {
		return ErrObserverRequiresPostCommit
	}
	if capabilities&CapabilityTransformer != 0 && r.options.MaxTransformBytes == 0 {
		return ErrLimitExceeded
	}
	return r.definePoint(point, capabilities, false)
}

// DefinePostCommitPoint adds a notification-only lifecycle seam. The core
// must invoke it only after its durable transaction commits and locks release.
func (r *Registry) DefinePostCommitPoint(point Point) error {
	if r == nil || !safeToken(string(point)) {
		return ErrInvalidPoint
	}
	return r.definePoint(point, CapabilityObserver, true)
}

func (r *Registry) definePoint(point Point, capabilities Capability, postCommit bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrInvalidRegistration
	}
	if _, exists := r.capabilities[point]; exists {
		return ErrDuplicatePoint
	}
	if len(r.capabilities)-len(builtInPoints) >= r.options.MaxCustomPoints {
		return ErrLimitExceeded
	}
	r.capabilities[point] = capabilities
	if postCommit {
		r.postCommit[point] = struct{}{}
	}
	return nil
}

// EnableCapabilities adds hook contracts to an existing built-in or custom
// point before Seal. Capabilities are additive and cannot be removed.
func (r *Registry) EnableCapabilities(point Point, capabilities Capability) error {
	if r == nil || !safeToken(string(point)) || capabilities == 0 || capabilities&^allCapabilities != 0 {
		return ErrInvalidPoint
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrInvalidRegistration
	}
	current, exists := r.capabilities[point]
	if !exists {
		return ErrUnknownPoint
	}
	_, isPostCommit := r.postCommit[point]
	if isPostCommit && capabilities&^CapabilityObserver != 0 {
		return ErrPostCommitObserverOnly
	}
	if !isPostCommit && capabilities&CapabilityObserver != 0 {
		return ErrObserverRequiresPostCommit
	}
	if capabilities&CapabilityTransformer != 0 && r.options.MaxTransformBytes == 0 {
		return ErrLimitExceeded
	}
	r.capabilities[point] = current | capabilities
	return nil
}

func (r *Registry) Register(reg Registration) error {
	if r == nil || !safeToken(reg.Name) || !safeToken(reg.Version) || !safeToken(string(reg.Point)) {
		return ErrInvalidRegistration
	}
	count := 0
	if reg.Guard != nil {
		count++
	}
	if reg.Observer != nil {
		count++
	}
	if reg.Orderer != nil {
		count++
	}
	if reg.Transformer != nil {
		count++
	}
	if count != 1 {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrInvalidRegistration
	}
	capabilities, exists := r.capabilities[reg.Point]
	if !exists {
		return ErrUnknownPoint
	}
	if (reg.Guard != nil && capabilities&CapabilityGuard == 0) ||
		(reg.Observer != nil && capabilities&CapabilityObserver == 0) ||
		(reg.Orderer != nil && capabilities&CapabilityOrderer == 0) ||
		(reg.Transformer != nil && capabilities&CapabilityTransformer == 0) {
		return ErrInvalidRegistration
	}
	if _, exists := r.names[reg.Name]; exists {
		return ErrDuplicateName
	}
	if len(r.byPoint[reg.Point]) >= r.options.MaxRegistrationsPerPoint {
		return ErrLimitExceeded
	}
	if reg.Transformer != nil && r.options.MaxTransformBytes == 0 {
		return ErrLimitExceeded
	}
	r.names[reg.Name] = struct{}{}
	r.byPoint[reg.Point] = append(r.byPoint[reg.Point], hook{Registration: reg})
	return nil
}

// Seal freezes registrations and establishes a stable call order.
func (r *Registry) Seal() error {
	if r == nil {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for point := range r.byPoint {
		sort.Slice(r.byPoint[point], func(i, j int) bool {
			a, b := r.byPoint[point][i], r.byPoint[point][j]
			if a.Priority != b.Priority {
				return a.Priority < b.Priority
			}
			return a.Name < b.Name
		})
	}
	manifest := RegistryManifest{
		APIVersion: RegistryAPIVersion,
		Limits: RegistryManifestLimits{
			MaxCustomPoints:          r.options.MaxCustomPoints,
			MaxRegistrationsPerPoint: r.options.MaxRegistrationsPerPoint,
			MaxTransformBytes:        r.options.MaxTransformBytes,
			MaxEventPayloadBytes:     r.options.MaxEventPayloadBytes,
		},
		Points:        make([]PointManifestEntry, 0, len(r.capabilities)),
		Registrations: make([]HookManifestEntry, 0, len(r.names)),
	}
	points := make([]Point, 0, len(r.capabilities))
	for point := range r.capabilities {
		points = append(points, point)
	}
	slices.Sort(points)
	for _, point := range points {
		_, postCommit := r.postCommit[point]
		manifest.Points = append(manifest.Points, PointManifestEntry{
			Point: point, Capabilities: r.capabilities[point], PostCommit: postCommit,
		})
		for _, item := range r.byPoint[point] {
			manifest.Registrations = append(manifest.Registrations, HookManifestEntry{
				Name: item.Name, Version: item.Version, Point: point, Priority: item.Priority,
				Capability: registrationCapability(item.Registration),
			})
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	r.manifest = manifest
	r.revision = hex.EncodeToString(digest[:])
	r.sealed = true
	return nil
}

// Manifest returns a defensive copy of the callback-free sealed registry manifest.
func (r *Registry) Manifest() (RegistryManifest, error) {
	if r == nil {
		return RegistryManifest{}, ErrUnknownPoint
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.sealed {
		return RegistryManifest{}, ErrInvalidRegistration
	}
	manifest := r.manifest
	manifest.Points = slices.Clone(manifest.Points)
	manifest.Registrations = slices.Clone(manifest.Registrations)
	return manifest, nil
}

// Revision returns the SHA-256 of the deterministic sealed manifest.
// Callback authors must bump Registration.Version when callback semantics change.
func (r *Registry) Revision() (string, error) {
	if r == nil {
		return "", ErrUnknownPoint
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.sealed {
		return "", ErrInvalidRegistration
	}
	return r.revision, nil
}

func (r *Registry) registrations(point Point) ([]hook, error) {
	if r == nil {
		return nil, ErrUnknownPoint
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.capabilities[point]; !exists {
		return nil, ErrUnknownPoint
	}
	if !r.sealed {
		return nil, ErrInvalidRegistration
	}
	return slices.Clone(r.byPoint[point]), nil
}

func (r *Registry) eventForCallbacks(event Event) Event {
	event.APIVersion = RegistryAPIVersion
	event.RegistryRevision = r.revision
	return event
}

// Check runs every guard at a veto-only point. A failing guard, panic, or
// context cancellation is a denial; an allow never replaces core validation.
func (r *Registry) Check(ctx context.Context, event Event) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := r.requireCapability(event.Point, CapabilityGuard); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrDeadlineRequired
	}
	registered, err := r.registrations(event.Point)
	if err != nil {
		return err
	}
	if name, oversized := oversizedEventPayload(registered, CapabilityGuard, len(event.Payload), r.options.MaxEventPayloadBytes); oversized {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrGuardDenied, name, err)
		}
		return fmt.Errorf("%w: %s: %w", ErrGuardDenied, name, ErrEventTooLarge)
	}
	event = r.eventForCallbacks(event)
	for _, item := range registered {
		if item.Guard == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrGuardDenied, item.Name, err)
		}
		decision, callErr := callGuard(ctx, item.Guard, event.clone())
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrGuardDenied, item.Name, err)
		}
		if callErr != nil {
			return fmt.Errorf("%w: %s: %v", ErrGuardDenied, item.Name, callErr)
		}
		if !decision.Allowed {
			if !safeToken(decision.Code) {
				decision.Code = "hook_denied"
			}
			return fmt.Errorf("%w: %s: %s", ErrGuardDenied, item.Name, decision.Code)
		}
	}
	return nil
}

// OrderCandidates permits only an exact permutation of the core-filtered IDs.
func (r *Registry) OrderCandidates(ctx context.Context, event Event, eligible []string) ([]string, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	if err := r.requireCapability(event.Point, CapabilityOrderer); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrDeadlineRequired
	}
	registered, err := r.registrations(event.Point)
	if err != nil {
		return nil, err
	}
	if name, oversized := oversizedEventPayload(registered, CapabilityOrderer, len(event.Payload), r.options.MaxEventPayloadBytes); oversized {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidPermutation, name, err)
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidPermutation, name, ErrEventTooLarge)
	}
	event = r.eventForCallbacks(event)
	current := slices.Clone(eligible)
	if err := validatePermutation(eligible, current); err != nil {
		return nil, err
	}
	for _, item := range registered {
		if item.Orderer == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidPermutation, item.Name, err)
		}
		next, callErr := callOrderer(ctx, item.Orderer, event.clone(), slices.Clone(current))
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidPermutation, item.Name, err)
		}
		if callErr != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidPermutation, item.Name, ErrHookFailed)
		}
		if err := validatePermutation(eligible, next); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidPermutation, item.Name)
		}
		current = slices.Clone(next)
	}
	return current, nil
}

// Transform applies an ordered chain of point-scoped transformations. The
// core supplies validate and remains responsible for enforcing all business
// invariants after every transformation. Callback and validator error details
// are never returned to callers.
func (r *Registry) Transform(ctx context.Context, event Event, input []byte, validate TransformValidator) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	if validate == nil {
		return nil, ErrInvalidValidator
	}
	if err := r.requireCapability(event.Point, CapabilityTransformer); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrDeadlineRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransformation, err)
	}
	if r.options.MaxTransformBytes == 0 {
		return nil, ErrLimitExceeded
	}
	if len(input) > r.options.MaxTransformBytes {
		return nil, ErrTransformTooLarge
	}
	registered, err := r.registrations(event.Point)
	if err != nil {
		return nil, err
	}
	if name, oversized := oversizedEventPayload(registered, CapabilityTransformer, len(event.Payload), r.options.MaxEventPayloadBytes); oversized {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidTransformation, name, err)
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidTransformation, name, ErrEventTooLarge)
	}
	event = r.eventForCallbacks(event)
	current := slices.Clone(input)
	if err := validate(slices.Clone(current)); err != nil {
		return nil, ErrInvalidTransformation
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransformation, err)
	}
	for _, item := range registered {
		if item.Transformer == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidTransformation, item.Name, err)
		}
		next, callErr := callTransformer(ctx, item.Transformer, event.clone(), slices.Clone(current))
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidTransformation, item.Name, err)
		}
		if callErr != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidTransformation, item.Name, ErrHookFailed)
		}
		if len(next) > r.options.MaxTransformBytes {
			return nil, fmt.Errorf("%w: %s", ErrTransformTooLarge, item.Name)
		}
		if err := validate(slices.Clone(next)); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidTransformation, item.Name)
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidTransformation, item.Name, err)
		}
		current = slices.Clone(next)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTransformation, err)
	}
	return slices.Clone(current), nil
}

// Notify synchronously invokes observers after a durable commit and returns
// failures for logging/retry. Call it from a bounded post-commit worker so an
// observer cannot block the business request. Observer errors never alter the
// already committed result.
func (r *Registry) Notify(ctx context.Context, event Event) []error {
	if ctx == nil {
		return []error{ErrInvalidContext}
	}
	if err := r.requireCapability(event.Point, CapabilityObserver); err != nil {
		return []error{err}
	}
	if _, ok := ctx.Deadline(); !ok {
		return []error{ErrDeadlineRequired}
	}
	registered, err := r.registrations(event.Point)
	if err != nil {
		return []error{err}
	}
	if name, oversized := oversizedEventPayload(registered, CapabilityObserver, len(event.Payload), r.options.MaxEventPayloadBytes); oversized {
		if err := ctx.Err(); err != nil {
			return []error{fmt.Errorf("%s: %w", name, err)}
		}
		return []error{fmt.Errorf("%s: %w", name, ErrEventTooLarge)}
	}
	event = r.eventForCallbacks(event)
	var failures []error
	for _, item := range registered {
		if item.Observer == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.Name, err))
			continue
		}
		callErr := callObserver(ctx, item.Observer, event.clone())
		if ctxErr := ctx.Err(); ctxErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.Name, ctxErr))
			continue
		}
		if callErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.Name, ErrObserverFailed))
		}
	}
	return failures
}

func oversizedEventPayload(registered []hook, capability Capability, payloadBytes, limit int) (string, bool) {
	if payloadBytes <= limit {
		return "", false
	}
	for _, item := range registered {
		if (item.Guard != nil && capability == CapabilityGuard) ||
			(item.Orderer != nil && capability == CapabilityOrderer) ||
			(item.Transformer != nil && capability == CapabilityTransformer) ||
			(item.Observer != nil && capability == CapabilityObserver) {
			return item.Name, true
		}
	}
	return "", false
}

func registrationCapability(reg Registration) Capability {
	switch {
	case reg.Guard != nil:
		return CapabilityGuard
	case reg.Orderer != nil:
		return CapabilityOrderer
	case reg.Transformer != nil:
		return CapabilityTransformer
	case reg.Observer != nil:
		return CapabilityObserver
	default:
		return 0
	}
}

func (r *Registry) requireCapability(point Point, capability Capability) error {
	if r == nil {
		return ErrUnknownPoint
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, exists := r.capabilities[point]
	if !exists {
		return ErrUnknownPoint
	}
	if registered&capability == 0 {
		return ErrUnsupportedCapability
	}
	return nil
}

func validatePermutation(eligible, ordered []string) error {
	if len(eligible) != len(ordered) {
		return ErrInvalidPermutation
	}
	want := make(map[string]int, len(eligible))
	for _, id := range eligible {
		if id == "" {
			return ErrInvalidPermutation
		}
		if _, exists := want[id]; exists {
			return ErrDuplicateCandidateID
		}
		want[id] = 1
	}
	for _, id := range ordered {
		if want[id] == 0 {
			return ErrInvalidPermutation
		}
		want[id]--
	}
	for _, count := range want {
		if count != 0 {
			return ErrInvalidPermutation
		}
	}
	return nil
}

func safeToken(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func callGuard(ctx context.Context, fn Guard, event Event) (decision GuardDecision, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("hook panicked")
		}
	}()
	return fn(ctx, event), nil
}

func callOrderer(ctx context.Context, fn CandidateOrderer, event Event, ids []string) (ordered []string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("hook panicked")
		}
	}()
	return fn(ctx, event, ids)
}

func callObserver(ctx context.Context, fn Observer, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("hook panicked")
		}
	}()
	return fn(ctx, event)
}

func callTransformer(ctx context.Context, fn Transformer, event Event, input []byte) (output []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New("hook panicked")
		}
	}()
	return fn(ctx, event, input)
}

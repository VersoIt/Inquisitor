package risk

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const KillSwitchArtifactSchemaVersion = "inquisitor.risk_kill_switch.v1"

const (
	KillSwitchArtifactActionState    = "state"
	KillSwitchArtifactActionList     = "list"
	KillSwitchArtifactActionActivate = "activate"
	KillSwitchArtifactActionRelease  = "release"
)

type BuildKillSwitchArtifactRequest struct {
	CreatedAt  time.Time
	ConfigPath string
	Action     string
	Query      *KillSwitchEventQuery
	State      *KillSwitchState
	Events     []KillSwitchEvent
	Event      *KillSwitchEvent
}

type KillSwitchArtifact struct {
	SchemaVersion string                    `json:"schema_version"`
	CreatedAt     time.Time                 `json:"created_at"`
	ConfigPath    string                    `json:"config_path"`
	Action        string                    `json:"action"`
	Query         *KillSwitchArtifactQuery  `json:"query,omitempty"`
	State         *KillSwitchArtifactState  `json:"state,omitempty"`
	Events        []KillSwitchArtifactEvent `json:"events,omitempty"`
	Event         *KillSwitchArtifactEvent  `json:"event,omitempty"`
}

type KillSwitchArtifactQuery struct {
	EventID string     `json:"event_id,omitempty"`
	Active  *bool      `json:"active,omitempty"`
	Source  string     `json:"source,omitempty"`
	Start   *time.Time `json:"start,omitempty"`
	End     *time.Time `json:"end,omitempty"`
	Limit   int        `json:"limit,omitempty"`
}

type KillSwitchArtifactState struct {
	Active    bool       `json:"active"`
	Reason    string     `json:"reason,omitempty"`
	Source    string     `json:"source,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type KillSwitchArtifactEvent struct {
	EventID   string    `json:"event_id"`
	Active    bool      `json:"active"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

func BuildKillSwitchArtifact(req BuildKillSwitchArtifactRequest) (KillSwitchArtifact, error) {
	artifact := KillSwitchArtifact{
		SchemaVersion: KillSwitchArtifactSchemaVersion,
		CreatedAt:     req.CreatedAt.UTC(),
		ConfigPath:    strings.TrimSpace(req.ConfigPath),
		Action:        strings.ToLower(strings.TrimSpace(req.Action)),
	}
	if req.Query != nil {
		artifact.Query = killSwitchArtifactQueryFromDomain(*req.Query)
	}
	if req.State != nil {
		artifact.State = killSwitchArtifactStateFromDomain(*req.State)
	}
	for _, event := range req.Events {
		artifact.Events = append(artifact.Events, killSwitchArtifactEventFromDomain(event))
	}
	if req.Event != nil {
		event := killSwitchArtifactEventFromDomain(*req.Event)
		artifact.Event = &event
	}
	if err := ValidateKillSwitchArtifact(artifact); err != nil {
		return KillSwitchArtifact{}, err
	}
	return artifact, nil
}

func ValidateKillSwitchArtifact(artifact KillSwitchArtifact) error {
	var problems []string
	if artifact.SchemaVersion != KillSwitchArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+KillSwitchArtifactSchemaVersion)
	}
	if artifact.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if strings.TrimSpace(artifact.ConfigPath) == "" {
		problems = append(problems, "config_path is required")
	} else if artifact.ConfigPath != strings.TrimSpace(artifact.ConfigPath) {
		problems = append(problems, "config_path must be trimmed")
	}
	if !knownKillSwitchArtifactAction(artifact.Action) {
		problems = append(problems, "action must be state, list, activate, or release")
	}
	if artifact.Query != nil {
		problems = append(problems, validateKillSwitchArtifactQueryProblems(*artifact.Query)...)
	}
	if artifact.State != nil {
		problems = append(problems, validateKillSwitchArtifactStateProblems(*artifact.State)...)
	}
	if artifact.Event != nil {
		problems = append(problems, validateKillSwitchArtifactEventProblems("event", *artifact.Event)...)
	}
	for index, event := range artifact.Events {
		problems = append(problems, validateKillSwitchArtifactEventProblems(fmt.Sprintf("events[%d]", index), event)...)
	}
	problems = append(problems, validateKillSwitchArtifactActionProblems(artifact)...)
	if len(problems) > 0 {
		return errors.New("kill switch artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func KillSwitchStateFromEvent(event KillSwitchEvent) KillSwitchState {
	return KillSwitchState{
		Active:    event.Active,
		Reason:    event.Reason,
		Source:    event.Source,
		UpdatedAt: event.CreatedAt.UTC(),
	}
}

func knownKillSwitchArtifactAction(action string) bool {
	switch action {
	case KillSwitchArtifactActionState, KillSwitchArtifactActionList, KillSwitchArtifactActionActivate, KillSwitchArtifactActionRelease:
		return true
	default:
		return false
	}
}

func killSwitchArtifactQueryFromDomain(query KillSwitchEventQuery) *KillSwitchArtifactQuery {
	artifact := &KillSwitchArtifactQuery{
		EventID: strings.TrimSpace(query.EventID),
		Source:  strings.TrimSpace(query.Source),
		Limit:   query.Limit,
	}
	if query.Active != nil {
		active := *query.Active
		artifact.Active = &active
	}
	if !query.Start.IsZero() {
		start := query.Start.UTC()
		artifact.Start = &start
	}
	if !query.End.IsZero() {
		end := query.End.UTC()
		artifact.End = &end
	}
	return artifact
}

func killSwitchArtifactStateFromDomain(state KillSwitchState) *KillSwitchArtifactState {
	artifact := &KillSwitchArtifactState{
		Active: state.Active,
		Reason: strings.TrimSpace(state.Reason),
		Source: strings.TrimSpace(state.Source),
	}
	if !state.UpdatedAt.IsZero() {
		updatedAt := state.UpdatedAt.UTC()
		artifact.UpdatedAt = &updatedAt
	}
	return artifact
}

func killSwitchArtifactEventFromDomain(event KillSwitchEvent) KillSwitchArtifactEvent {
	return KillSwitchArtifactEvent{
		EventID:   strings.TrimSpace(event.EventID),
		Active:    event.Active,
		Reason:    strings.TrimSpace(event.Reason),
		Source:    strings.TrimSpace(event.Source),
		CreatedAt: event.CreatedAt.UTC(),
	}
}

func validateKillSwitchArtifactQueryProblems(query KillSwitchArtifactQuery) []string {
	var active *bool
	if query.Active != nil {
		value := *query.Active
		active = &value
	}
	var start time.Time
	if query.Start != nil {
		start = query.Start.UTC()
	}
	var end time.Time
	if query.End != nil {
		end = query.End.UTC()
	}
	domainQuery := KillSwitchEventQuery{
		EventID: query.EventID,
		Active:  active,
		Source:  query.Source,
		Start:   start,
		End:     end,
		Limit:   query.Limit,
	}
	if err := ValidateKillSwitchEventQuery(domainQuery); err != nil {
		return []string{"query." + err.Error()}
	}
	return nil
}

func validateKillSwitchArtifactStateProblems(state KillSwitchArtifactState) []string {
	var updatedAt time.Time
	if state.UpdatedAt != nil {
		updatedAt = state.UpdatedAt.UTC()
	}
	domainState := KillSwitchState{
		Active:    state.Active,
		Reason:    state.Reason,
		Source:    state.Source,
		UpdatedAt: updatedAt,
	}
	if err := ValidateKillSwitchState(domainState); err != nil {
		return []string{"state." + err.Error()}
	}
	return nil
}

func validateKillSwitchArtifactEventProblems(name string, event KillSwitchArtifactEvent) []string {
	domainEvent := KillSwitchEvent{
		EventID:   event.EventID,
		Active:    event.Active,
		Reason:    event.Reason,
		Source:    event.Source,
		CreatedAt: event.CreatedAt,
	}
	if err := ValidateKillSwitchEvent(domainEvent); err != nil {
		return []string{name + "." + err.Error()}
	}
	return nil
}

func validateKillSwitchArtifactActionProblems(artifact KillSwitchArtifact) []string {
	switch artifact.Action {
	case KillSwitchArtifactActionState:
		return validateKillSwitchArtifactStateActionProblems(artifact)
	case KillSwitchArtifactActionList:
		return validateKillSwitchArtifactListActionProblems(artifact)
	case KillSwitchArtifactActionActivate:
		return validateKillSwitchArtifactWriteActionProblems(artifact, true)
	case KillSwitchArtifactActionRelease:
		return validateKillSwitchArtifactWriteActionProblems(artifact, false)
	default:
		return nil
	}
}

func validateKillSwitchArtifactStateActionProblems(artifact KillSwitchArtifact) []string {
	var problems []string
	if artifact.State == nil {
		problems = append(problems, "state action requires state")
	}
	if artifact.Query != nil {
		problems = append(problems, "state action must not include query")
	}
	if artifact.Event != nil {
		problems = append(problems, "state action must not include event")
	}
	if len(artifact.Events) != 0 {
		problems = append(problems, "state action must not include events")
	}
	return problems
}

func validateKillSwitchArtifactListActionProblems(artifact KillSwitchArtifact) []string {
	var problems []string
	if artifact.Query == nil {
		problems = append(problems, "list action requires query")
	} else if artifact.Query.Limit <= 0 {
		problems = append(problems, "list query.limit must be positive")
	} else if len(artifact.Events) > artifact.Query.Limit {
		problems = append(problems, "list events length must not exceed query.limit")
	}
	if artifact.State != nil {
		problems = append(problems, "list action must not include state")
	}
	if artifact.Event != nil {
		problems = append(problems, "list action must not include event")
	}
	return problems
}

func validateKillSwitchArtifactWriteActionProblems(artifact KillSwitchArtifact, active bool) []string {
	var problems []string
	if artifact.Event == nil {
		problems = append(problems, "write action requires event")
		return problems
	}
	if artifact.Event.Active != active {
		problems = append(problems, "write event active must match action")
	}
	if artifact.State == nil {
		problems = append(problems, "write action requires state")
	} else {
		if artifact.State.Active != artifact.Event.Active ||
			artifact.State.Reason != artifact.Event.Reason ||
			artifact.State.Source != artifact.Event.Source ||
			artifact.State.UpdatedAt == nil ||
			!artifact.State.UpdatedAt.Equal(artifact.Event.CreatedAt.UTC()) {
			problems = append(problems, "write state must mirror event")
		}
	}
	if artifact.Query != nil {
		problems = append(problems, "write action must not include query")
	}
	if len(artifact.Events) != 0 {
		problems = append(problems, "write action must not include events")
	}
	return problems
}
